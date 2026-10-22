#include "deskhand_rdp.h"

#include <freerdp/freerdp.h>
#include <freerdp/client.h>
#include <freerdp/client/channels.h>
#include <freerdp/client/rdpgfx.h>
#include <freerdp/client/disp.h>
#include <freerdp/constants.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/gdi/gfx.h>
#include <freerdp/session.h>
#include <freerdp/settings.h>
#include <freerdp/update.h>
#include <freerdp/transport_io.h>
#include <winpr/synch.h>
#include <openssl/crypto.h>
#include <openssl/pem.h>
#include <openssl/provider.h>
#include <openssl/x509.h>

#include <limits.h>
#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <arpa/inet.h>
#include <dns_sd.h>
#include <errno.h>
#include <dlfcn.h>
#include <poll.h>
#include <time.h>

typedef struct dh_session dh_session;
#define DH_GFX_MAX_SURFACES 16u
#define DH_GFX_MAX_BYTES ((size_t)256 * 1024 * 1024)
typedef struct {
    rdpClientContext common; /* FreeRDP client/common requires this prefix. */
    dh_session* owner;
} dh_context;

struct dh_session {
    rdpContext* context; /* Stable until free; stop never destroys this. */
    pthread_mutex_t frame_lock;
    pthread_mutex_t resize_lock;
    DispClientContext* disp;
    bool disp_ready, resize_pending;
    uint64_t max_monitor_area;
    DISPLAY_CONTROL_MONITOR_LAYOUT requested_layout;

    atomic_bool used, active, stopping;
    atomic_int state;
    atomic_uint error, session_id;
    uint8_t pin[32];
    uint8_t* frame;
    size_t frame_bytes;
    uint32_t width, height, stride;
    uint64_t sequence;
    bool subscribed_connected, subscribed_disconnected;
    pTCPConnect tcp_connect;
    pcRdpgfxCreateSurface gfx_create_surface;
    pcRdpgfxDeleteSurface gfx_delete_surface;
    RdpgfxClientContext* gfx;
    struct { UINT16 id; size_t bytes; } surfaces[DH_GFX_MAX_SURFACES];
    size_t surface_bytes, surface_count;
};

static dh_session* owner(rdpContext* context) {
    return ((dh_context*)context)->owner;
}

static void fail(dh_session* s, uint32_t error) {
    unsigned expected = 0;
    (void)atomic_compare_exchange_strong(&s->error, &expected, error);
}

typedef struct {
    char address[16][INET6_ADDRSTRLEN + 16];
    size_t count;
    pthread_mutex_t lock;
    atomic_bool cancelled, finished;
    dh_session* session;
    DNSServiceRef query;
    uint64_t deadline;
} dh_dns_result;

static void dns_reply(DNSServiceRef ref, DNSServiceFlags flags, uint32_t index,
                      DNSServiceErrorType error, const char* hostname,
                      const struct sockaddr* address, uint32_t ttl, void* context) {
    (void)ref; (void)hostname; (void)ttl;
    dh_dns_result* result = context;
    pthread_mutex_lock(&result->lock);
    size_t previous_count = result->count;
    if (!error && (flags & kDNSServiceFlagsAdd) && address && result->count < 16) {
        char* out = result->address[result->count];
        if (address->sa_family == AF_INET) {
            const struct sockaddr_in* v4 = (const struct sockaddr_in*)address;
            if (inet_ntop(AF_INET, &v4->sin_addr, out, sizeof(result->address[0])))
                ++result->count;
        } else if (address->sa_family == AF_INET6) {
            const struct sockaddr_in6* v6 = (const struct sockaddr_in6*)address;
            if (inet_ntop(AF_INET6, &v6->sin6_addr, out, sizeof(result->address[0]))) {
                if (IN6_IS_ADDR_LINKLOCAL(&v6->sin6_addr)) {
                    size_t length = strlen(out);
                    (void)snprintf(out + length, sizeof(result->address[0]) - length,
                                   "%%%u", v6->sin6_scope_id ? v6->sin6_scope_id : index);
                }
                ++result->count;
            }
        }
    }
    if (result->count > previous_count) {
        for (size_t i = 0; i < previous_count; ++i) {
            if (!strcmp(result->address[i], result->address[previous_count])) {
                --result->count;
                break;
            }
        }
    }
    pthread_mutex_unlock(&result->lock);
}

static uint64_t monotonic_ms(void) {
    struct timespec now;
    clock_gettime(CLOCK_MONOTONIC, &now);
    return (uint64_t)now.tv_sec * 1000 + (uint64_t)now.tv_nsec / 1000000;
}

/* DNS-SD owns a live query, not a finite batch. This worker keeps processing
 * delayed A/AAAA replies even while the caller is in a numeric TCP attempt.
 * Only this thread processes the ref; the caller joins before deallocation and the
 * callback context leaves scope. MoreComing is deliberately not completion. */
static void* resolve_addresses(void* arg) {
    dh_dns_result* result = arg;
    struct pollfd fd = {.fd = DNSServiceRefSockFD(result->query), .events = POLLIN};
    while (!atomic_load(&result->cancelled) && !atomic_load(&result->session->stopping)) {
        uint64_t current = monotonic_ms();
        if (current >= result->deadline || fd.fd < 0) break;
        int wait = (int)((result->deadline - current) < 25 ? result->deadline - current : 25);
        int ready = poll(&fd, 1, wait);
        if (ready < 0 && errno == EINTR) continue;
        if (ready < 0 || (ready > 0 && !(fd.revents & POLLIN))) break;
        if (ready > 0 && DNSServiceProcessResult(result->query) != kDNSServiceErr_NoError) break;
    }
    /* Caller cancels and joins before deallocation so no active numeric attempt
     * can outlive the query, including when the deadline expires in that attempt. */
    atomic_store(&result->finished, true);
    return NULL;
}

/* The upstream TCP connector calls blocking getaddrinfo (tcp.c:786). Resolve
 * asynchronously with macOS DNS-SD, polling stop, then delegate only numeric
 * addresses to FreeRDP. Keep settings->ServerHostname intact for NLA/TLS/SNI. */
static int tcp_connect(rdpContext* context, rdpSettings* settings, const char* hostname,
                       int port, DWORD timeout) {
    dh_session* s = owner(context);
    if (atomic_load(&s->stopping)) return -1;
    struct in6_addr literal;
    if (inet_pton(AF_INET, hostname, &literal) == 1 ||
        inet_pton(AF_INET6, hostname, &literal) == 1)
        return s->tcp_connect(context, settings, hostname, port, timeout);
    dh_dns_result result = {.session = s,
        .deadline = monotonic_ms() + (timeout ? timeout : 10000)};
    atomic_init(&result.cancelled, false);
    atomic_init(&result.finished, false);
    if (pthread_mutex_init(&result.lock, NULL) != 0) { fail(s, DH_RDP_ERROR_MEMORY); return -1; }
    DNSServiceRef query = NULL;
    DNSServiceErrorType error = DNSServiceGetAddrInfo(&query, 0, 0,
        kDNSServiceProtocol_IPv4 | kDNSServiceProtocol_IPv6, hostname, dns_reply, &result);
    if (error) {
        pthread_mutex_destroy(&result.lock);
        fail(s, DH_RDP_ERROR_DNS);
        return -1;
    }
    result.query = query;
    pthread_t resolver;
    if (pthread_create(&resolver, NULL, resolve_addresses, &result) != 0) {
        DNSServiceRefDeallocate(query);
        pthread_mutex_destroy(&result.lock);
        fail(s, DH_RDP_ERROR_MEMORY);
        return -1;
    }
    int connected = -1;
    const UINT32 previous_error = freerdp_get_last_error(context);
    size_t next = 0;
    while (!atomic_load(&s->stopping)) {
        uint64_t current = monotonic_ms();
        if (current >= result.deadline) break;
        char candidate[sizeof(result.address[0])] = {0};
        pthread_mutex_lock(&result.lock);
        if (next < result.count) memcpy(candidate, result.address[next++], sizeof(candidate));
        pthread_mutex_unlock(&result.lock);
        if (candidate[0]) {
            /* Reserve time for later families even with a short total deadline.
             * FreeRDP's numeric connector also waits on the session abort event. */
            uint64_t slice = (result.deadline - current) / 2;
            if (!slice) slice = 1;
            if (slice > 250) slice = 250;
            connected = s->tcp_connect(context, settings, candidate, port, (DWORD)slice);
            if (connected >= 0) break;
        } else {
            if (atomic_load(&result.finished)) break;
            struct timespec pause = {.tv_nsec = 5000000};
            nanosleep(&pause, NULL);
        }
    }
    atomic_store(&result.cancelled, true);
    pthread_join(resolver, NULL);
    DNSServiceRefDeallocate(query);
    /* A failed candidate sets FreeRDP's last error. Do not carry it into a
     * successful fallback connection, or overwrite a concurrent stop error. */
    if (connected >= 0 && !atomic_load(&s->stopping))
        freerdp_set_last_error(context, previous_error);
    if (!result.count && !atomic_load(&s->stopping)) fail(s, DH_RDP_ERROR_DNS);
    pthread_mutex_destroy(&result.lock);
    return connected;
}

static bool frame_size(uint32_t width, uint32_t height, size_t* bytes) {
    if (!width || !height || width > DH_RDP_MAX_DIMENSION || height > DH_RDP_MAX_DIMENSION)
        return false;
    const uint64_t size = (uint64_t)width * height * 4;
    if (size > DH_RDP_MAX_FRAME_BYTES)
        return false;
    *bytes = (size_t)size;
    return true;
}

static int unhex(char ch) {
    if (ch >= '0' && ch <= '9') return ch - '0';
    if (ch >= 'a' && ch <= 'f') return ch - 'a' + 10;
    if (ch >= 'A' && ch <= 'F') return ch - 'A' + 10;
    return -1;
}

static bool parse_pin(const char* text, uint8_t pin[32]) {
    if (!text) return false;
    size_t n = strnlen(text, 96);
    if (n != 64 && n != 95) return false;
    for (size_t i = 0; i < 32; i++) {
        size_t offset = i * (n == 95 ? 3 : 2);
        int high = unhex(text[offset]), low = unhex(text[offset + 1]);
        if (high < 0 || low < 0) return false;
        if (n == 95 && i < 31 && text[offset + 2] != ':') return false;
        pin[i] = (uint8_t)((high << 4) | low);
    }
    return true;
}

/* FreeRDP 3.32.2 passes the full chain in PEM. X509_digest hashes the leaf's
 * canonical DER, not PEM text or its public key. ExternalCertificateManagement
 * invokes this before the ordinary PKI fast path (tls.c:1863). */
static int verify_certificate(freerdp* instance, const BYTE* data, size_t length,
                              const char* hostname, UINT16 port, DWORD flags) {
    (void)hostname; (void)port;
    dh_session* s = owner(instance->context);
    int accepted = 0;
    if (data && length && length <= INT_MAX && flags == 0 && !atomic_load(&s->stopping)) {
        BIO* bio = BIO_new_mem_buf(data, (int)length);
        X509* cert = bio ? PEM_read_bio_X509(bio, NULL, NULL, NULL) : NULL;
        unsigned char digest[EVP_MAX_MD_SIZE];
        unsigned int size = 0;
        if (cert && X509_digest(cert, EVP_sha256(), digest, &size) && size == 32 &&
            CRYPTO_memcmp(digest, s->pin, 32) == 0)
            accepted = 2; /* Session only; never write a certificate/TOFU store. */
        OPENSSL_cleanse(digest, sizeof(digest));
        X509_free(cert);
        BIO_free(bio);
    }
    if (!accepted) fail(s, DH_RDP_ERROR_CERTIFICATE);
    return accepted;
}

static BOOL authenticate(freerdp* instance, char** user, char** password, char** domain,
                         rdp_auth_reason reason) {
    (void)instance; (void)user; (void)password; (void)domain; (void)reason;
    return FALSE; /* Credentials must be supplied by the in-memory ABI, never a prompt. */
}

static BOOL redirect(freerdp* instance) {
    fail(owner(instance->context), DH_RDP_ERROR_SECURITY);
    return FALSE; /* Only the endpoint selected by the paired agent is authorized. */
}

static void clear_password(dh_session* s) {
    char* password = freerdp_settings_get_string_writable(s->context->settings, FreeRDP_Password);
    if (password) OPENSSL_cleanse(password, strlen(password));
    (void)freerdp_settings_set_string(s->context->settings, FreeRDP_Password, NULL);
}

static BOOL save_session_info(rdpContext* context, UINT32 type, const void* data) {
    if ((type == INFO_TYPE_LOGON || type == INFO_TYPE_LOGON_LONG) && data) {
        const logon_info* info = data;
        atomic_store(&owner(context)->session_id, info->sessionId);
    } else if (type == INFO_TYPE_LOGON_EXTENDED_INF && data) {
        const logon_info_ex* info = data;
        if (info->haveCookie) atomic_store(&owner(context)->session_id, info->LogonId);
    }
    return TRUE;
}

static BOOL begin_paint(rdpContext* context) {
    rdpGdi* gdi = context->gdi;
    if (!gdi || !gdi->primary) return FALSE;
    gdi->primary->hdc->hwnd->invalid->null = TRUE;
    gdi->primary->hdc->hwnd->ninvalid = 0;
    return TRUE;
}

/* Only the FreeRDP event thread mutates GDI. Readers access our locked snapshot,
 * never GDI memory. Free the old allocation before allocating a different size
 * so resize cannot transiently retain two snapshots. */
static BOOL end_paint(rdpContext* context) {
    dh_session* s = owner(context);
    rdpGdi* gdi = context->gdi;
    if (!gdi || !gdi->primary) return FALSE;
    if (gdi->primary->hdc->hwnd->invalid->null) return TRUE;
    size_t bytes = 0;
    if (!frame_size((uint32_t)gdi->width, (uint32_t)gdi->height, &bytes) ||
        gdi->stride < (uint32_t)gdi->width * 4 || !gdi->primary_buffer) {
        fail(s, DH_RDP_ERROR_FRAME_SIZE);
        return FALSE;
    }
    pthread_mutex_lock(&s->frame_lock);
    if (s->frame_bytes != bytes) {
        free(s->frame);
        s->frame = NULL;
        s->frame_bytes = 0;
        s->width = s->height = s->stride = 0;
        s->frame = malloc(bytes);
    }
    if (!s->frame) {
        pthread_mutex_unlock(&s->frame_lock);
        fail(s, DH_RDP_ERROR_MEMORY);
        return FALSE;
    }
    s->width = (uint32_t)gdi->width;
    s->height = (uint32_t)gdi->height;
    s->stride = s->width * 4;
    s->frame_bytes = bytes;
    for (uint32_t y = 0; y < s->height; y++) {
        uint8_t* row = s->frame + (size_t)y * s->stride;
        memcpy(row, gdi->primary_buffer + (size_t)y * gdi->stride, s->stride);
        for (uint32_t x = 0; x < s->width; x++) row[(size_t)x * 4 + 3] = 255;
    }
    ++s->sequence;
    pthread_mutex_unlock(&s->frame_lock);
    return TRUE;
}

static BOOL desktop_resize(rdpContext* context) {
    size_t bytes = 0;
    const uint32_t w = freerdp_settings_get_uint32(context->settings, FreeRDP_DesktopWidth);
    const uint32_t h = freerdp_settings_get_uint32(context->settings, FreeRDP_DesktopHeight);
    if (!frame_size(w, h, &bytes)) {
        fail(owner(context), DH_RDP_ERROR_FRAME_SIZE);
        return FALSE;
    }
    return gdi_resize(context->gdi, w, h);
}

static UINT create_surface(RdpgfxClientContext* gfx, const RDPGFX_CREATE_SURFACE_PDU* surface) {
    rdpGdi* gdi = gfx->custom;
    dh_session* s = owner(gdi->context);
    size_t bytes;
    if (!frame_size(surface->width, surface->height, &bytes)) {
        fail(s, DH_RDP_ERROR_FRAME_SIZE);
        return ERROR_INVALID_DATA;
    }
    /* Match pinned gdi/gfx.c: aligned width/height, aligned BGRA stride + SIMD padding. */
    size_t width = ((size_t)surface->width + 15) & ~(size_t)15;
    size_t height = ((size_t)surface->height + 15) & ~(size_t)15;
    bytes = (((width * 4 + 15) & ~(size_t)15) + 16) * height;
    size_t slot = DH_GFX_MAX_SURFACES;
    for (size_t i = 0; i < DH_GFX_MAX_SURFACES; ++i) {
        /* The pinned channel deletes an old ID before a replacement. Reject
         * direct duplicate callbacks before upstream can overwrite/leak it. */
        if (s->surfaces[i].bytes && s->surfaces[i].id == surface->surfaceId)
            return ERROR_INVALID_DATA;
        if (!s->surfaces[i].bytes) slot = i;
    }
    if (slot == DH_GFX_MAX_SURFACES || bytes > DH_GFX_MAX_BYTES - s->surface_bytes) {
        fail(s, DH_RDP_ERROR_FRAME_SIZE);
        return ERROR_INVALID_DATA;
    }
    UINT rc = s->gfx_create_surface(gfx, surface);
    if (rc == CHANNEL_RC_OK) {
        s->surfaces[slot].id = surface->surfaceId;
        s->surfaces[slot].bytes = bytes;
        s->surface_bytes += bytes;
        ++s->surface_count;
    }
    return rc;
}

static UINT delete_surface(RdpgfxClientContext* gfx, const RDPGFX_DELETE_SURFACE_PDU* surface) {
    rdpGdi* gdi = gfx->custom;
    dh_session* s = owner(gdi->context);
    UINT rc = s->gfx_delete_surface(gfx, surface);
    if (rc == CHANNEL_RC_OK) {
        for (size_t i = 0; i < DH_GFX_MAX_SURFACES; ++i) {
            if (s->surfaces[i].bytes && s->surfaces[i].id == surface->surfaceId) {
                s->surface_bytes -= s->surfaces[i].bytes;
                s->surfaces[i].bytes = 0;
                --s->surface_count;
                break;
            }
        }
    }
    return rc;
}

static void release_surfaces(dh_session* s) {
    if (s->gfx) {
        /* OnClose normally deleted them already. Also cover partial connect and
         * channel disconnect with retained surfaces, before GDI is detached. */
        for (size_t i = 0; i < DH_GFX_MAX_SURFACES; ++i) {
            if (s->surfaces[i].bytes) {
                RDPGFX_DELETE_SURFACE_PDU pdu = {.surfaceId = s->surfaces[i].id};
                (void)s->gfx_delete_surface(s->gfx, &pdu);
            }
        }
        s->gfx->CreateSurface = s->gfx_create_surface;
        s->gfx->DeleteSurface = s->gfx_delete_surface;
    }
    memset(s->surfaces, 0, sizeof(s->surfaces));
    s->surface_bytes = s->surface_count = 0;
    s->gfx = NULL;
}

/* Channel callbacks and the run loop share only this locked display state.
 * Never call FreeRDP from the UI setter, nor retain a disconnected channel. */
int dh_rdp_resize(void* session, uint32_t width, uint32_t height, uint32_t desktop_scale) {
    dh_session* s = session;
    size_t bytes;
    if (!s || width < 200 || height < 200 || !frame_size(width, height, &bytes) ||
        desktop_scale < 100 || desktop_scale > 500 || atomic_load(&s->stopping)) return 0;
    DISPLAY_CONTROL_MONITOR_LAYOUT layout = {
        .Flags = DISPLAY_CONTROL_MONITOR_PRIMARY, .Width = width & ~1u, .Height = height,
        .DesktopScaleFactor = desktop_scale, .DeviceScaleFactor = desktop_scale >= 180 ? 180 : (desktop_scale >= 140 ? 140 : 100)
    };
    pthread_mutex_lock(&s->resize_lock);
    if (memcmp(&s->requested_layout, &layout, sizeof(layout)) != 0) {
        s->requested_layout = layout;
        s->resize_pending = true;
    }
    pthread_mutex_unlock(&s->resize_lock);
    return 1;
}

static UINT display_caps(DispClientContext* context, UINT32 monitors, UINT32 a, UINT32 b) {
    dh_session* s = context->custom;
    pthread_mutex_lock(&s->resize_lock);
    const uint64_t local_limit = DH_RDP_MAX_FRAME_BYTES / 4;
    const uint64_t area = (uint64_t)a * b;
    s->max_monitor_area = monitors && area > local_limit / monitors ? local_limit : area * monitors;
    s->disp_ready = monitors > 0 && s->max_monitor_area > 0;
    pthread_mutex_unlock(&s->resize_lock);
    return CHANNEL_RC_OK;
}

static UINT process_resize(dh_session* s) {
    UINT result = CHANNEL_RC_OK;
    pthread_mutex_lock(&s->resize_lock);
    if (s->disp && s->disp_ready && s->resize_pending) {
        DISPLAY_CONTROL_MONITOR_LAYOUT layout = s->requested_layout;
        s->resize_pending = false;
        const uint64_t area = (uint64_t)layout.Width * layout.Height;
        if (area > s->max_monitor_area) {
            const double factor = sqrt((double)s->max_monitor_area / (double)area);
            layout.Width = ((uint32_t)(layout.Width * factor)) & ~1u;
            layout.Height = (uint32_t)(layout.Height * factor);
            layout.DesktopScaleFactor = (UINT32)fmax(100, layout.DesktopScaleFactor * factor);
        }
        if (layout.Width >= 200 && layout.Height >= 200) {
            result = s->disp->SendMonitorLayout(s->disp, 1, &layout);
            if (result == ERROR_NOT_CONNECTED) { s->disp_ready = false; result = CHANNEL_RC_OK; }
        }
    }
    pthread_mutex_unlock(&s->resize_lock);
    return result;
}

static void channel_connected(void* context, const ChannelConnectedEventArgs* event) {
    rdpContext* ctx = context;
    if (strcmp(event->name, DISP_DVC_CHANNEL_NAME) == 0) {
        dh_session* s = owner(ctx);
        pthread_mutex_lock(&s->resize_lock);
        s->disp = event->pInterface;
        s->disp->custom = s;
        s->disp->DisplayControlCaps = display_caps;
        s->disp_ready = false;
        s->resize_pending = s->requested_layout.Width != 0;
        pthread_mutex_unlock(&s->resize_lock);
    } else if (strcmp(event->name, RDPGFX_DVC_CHANNEL_NAME) == 0) {
        if (!gdi_graphics_pipeline_init(ctx->gdi, event->pInterface)) {
            fail(owner(ctx), DH_RDP_ERROR_EVENT);
            (void)freerdp_abort_connect_context(ctx);
            return;
        }
        RdpgfxClientContext* gfx = event->pInterface;
        owner(ctx)->gfx_create_surface = gfx->CreateSurface;
        owner(ctx)->gfx_delete_surface = gfx->DeleteSurface;
        owner(ctx)->gfx = gfx;
        gfx->CreateSurface = create_surface;
        gfx->DeleteSurface = delete_surface;
    }
}

static void channel_disconnected(void* context, const ChannelDisconnectedEventArgs* event) {
    rdpContext* ctx = context;
    if (strcmp(event->name, DISP_DVC_CHANNEL_NAME) == 0) {
        dh_session* s = owner(ctx);
        pthread_mutex_lock(&s->resize_lock);
        s->disp = NULL;
        s->disp_ready = false;
        pthread_mutex_unlock(&s->resize_lock);
    } else if (strcmp(event->name, RDPGFX_DVC_CHANNEL_NAME) == 0 && ctx->gdi) {
        release_surfaces(owner(ctx));
        gdi_graphics_pipeline_uninit(ctx->gdi, event->pInterface);
    }
}

static BOOL pre_connect(freerdp* instance) {
    dh_session* s = owner(instance->context);
    /* freerdp_connect resets its abort event before PreConnect. Re-arm if stop
     * raced with that reset. Later stop calls set the same stable event. */
    if (atomic_load(&s->stopping)) {
        (void)freerdp_abort_connect_context(instance->context);
        return FALSE;
    }
    s->subscribed_connected = PubSub_SubscribeChannelConnected(instance->context->pubSub,
                                                              channel_connected) >= 0;
    s->subscribed_disconnected = PubSub_SubscribeChannelDisconnected(instance->context->pubSub,
                                                                    channel_disconnected) >= 0;
    return s->subscribed_connected && s->subscribed_disconnected;
}

static BOOL post_connect(freerdp* instance) {
    rdpContext* ctx = instance->context;
    size_t bytes = 0;
    if (!frame_size(freerdp_settings_get_uint32(ctx->settings, FreeRDP_DesktopWidth),
                    freerdp_settings_get_uint32(ctx->settings, FreeRDP_DesktopHeight), &bytes)) {
        fail(owner(ctx), DH_RDP_ERROR_FRAME_SIZE);
        return FALSE;
    }
    const uint32_t protocol = freerdp_settings_get_uint32(ctx->settings, FreeRDP_SelectedProtocol);
    /* [MS-RDPBCGR] selectedProtocol: HYBRID=2, HYBRID_EX=8. FreeRDP's names
     * are private in libfreerdp/core/nego.h; don't include private headers. */
    if (protocol != 0x00000002u && protocol != 0x00000008u) {
        fail(owner(ctx), DH_RDP_ERROR_SECURITY);
        return FALSE;
    }
    if (!gdi_init(instance, PIXEL_FORMAT_BGRA32)) return FALSE;
    ctx->update->BeginPaint = begin_paint;
    ctx->update->EndPaint = end_paint;
    ctx->update->DesktopResize = desktop_resize;
    return TRUE;
}

static void post_disconnect(freerdp* instance) {
    dh_session* s = owner(instance->context);
    if (s->subscribed_connected)
        PubSub_UnsubscribeChannelConnected(instance->context->pubSub, channel_connected);
    if (s->subscribed_disconnected)
        PubSub_UnsubscribeChannelDisconnected(instance->context->pubSub, channel_disconnected);
    s->subscribed_connected = s->subscribed_disconnected = false;
    release_surfaces(s);
    gdi_free(instance);
}

static BOOL client_new(freerdp* instance, rdpContext* context) {
    instance->PreConnect = pre_connect;
    instance->PostConnect = post_connect;
    /* Channel disconnect handlers need live GDI/codec state. Free it only after
     * freerdp_channels_close (PostFinalDisconnect), never in PostDisconnect. */
    instance->PostDisconnect = NULL;
    instance->PostFinalDisconnect = post_disconnect;
    instance->VerifyX509Certificate = verify_certificate;
    instance->AuthenticateEx = authenticate;
    instance->Redirect = redirect;
    instance->VerifyCertificateEx = NULL;
    instance->VerifyChangedCertificateEx = NULL;
    instance->GetAccessToken = NULL;
    instance->RetryDialog = NULL;
    instance->ChooseSmartcard = NULL;
    instance->PresentGatewayMessage = NULL;
    instance->LogonErrorInfo = NULL;
    context->update->SaveSessionInfo = save_session_info;
    return TRUE;
}

/* OpenSSL's legacy provider supplies NTLM's MD4/RC4. Resolve it next to this
 * engine, never from a developer's Homebrew directory or OPENSSL_MODULES.
 * Configure before FreeRDP creates its process-wide SSL state. */
static pthread_once_t crypto_path_once = PTHREAD_ONCE_INIT;
static bool crypto_path_ready = false;
static void configure_crypto_path(void) {
    Dl_info image = {0};
    char path[PATH_MAX];
    if (!dladdr((const void*)&dh_rdp_new, &image) || !image.dli_fname ||
        !realpath(image.dli_fname, path)) return;
    char* slash = strrchr(path, '/');
    if (!slash) return;
    *slash = '\0';
    crypto_path_ready = OSSL_PROVIDER_set_default_search_path(NULL, path) == 1;
}

void* dh_rdp_new(const char* host, uint16_t port, const char* username, const char* domain,
                 const char* password, const char* certificate_sha256,
                 uint32_t width, uint32_t height) {
    size_t bytes = 0;
    uint8_t pin[32];
    if (!host || !*host || host[0] == '/' || host[0] == '|' ||
        strnlen(host, 1025) > 1024 || !port || !username || !*username ||
        strnlen(username, 1025) > 1024 || (domain && strnlen(domain, 1025) > 1024) ||
        !password || strnlen(password, 65536) >= 65536 ||
        !parse_pin(certificate_sha256, pin) || width < 200 || height < 200 ||
        !frame_size(width, height, &bytes)) return NULL;
    pthread_once(&crypto_path_once, configure_crypto_path);
    if (!crypto_path_ready) return NULL;
    dh_session* s = calloc(1, sizeof(*s));
    if (!s) return NULL;
    atomic_init(&s->used, false);
    atomic_init(&s->active, false);
    atomic_init(&s->stopping, false);
    atomic_init(&s->state, DH_RDP_IDLE);
    atomic_init(&s->error, 0);
    atomic_init(&s->session_id, UINT32_MAX);
    memcpy(s->pin, pin, sizeof(pin));
    if (pthread_mutex_init(&s->frame_lock, NULL) != 0) { free(s); return NULL; }
    if (pthread_mutex_init(&s->resize_lock, NULL) != 0) { pthread_mutex_destroy(&s->frame_lock); free(s); return NULL; }
    RDP_CLIENT_ENTRY_POINTS entry = {0};
    entry.Version = RDP_CLIENT_INTERFACE_VERSION;
    entry.Size = sizeof(entry);
    entry.ContextSize = sizeof(dh_context);
    entry.ClientNew = client_new;
    s->context = freerdp_client_context_new(&entry);
    if (!s->context) { dh_rdp_free(s); return NULL; }
    ((dh_context*)s->context)->owner = s;
    rdpTransportIo io = *freerdp_get_io_callbacks(s->context);
    s->tcp_connect = io.TCPConnect;
    io.TCPConnect = tcp_connect;
    if (!s->tcp_connect || !freerdp_set_io_callbacks(s->context, &io)) goto bad;
    rdpSettings* settings = s->context->settings;
#define SET_BOOL(key, value) if (!freerdp_settings_set_bool(settings, FreeRDP_##key, value)) goto bad
#define SET_U32(key, value) if (!freerdp_settings_set_uint32(settings, FreeRDP_##key, value)) goto bad
#define SET_STR(key, value) if (!freerdp_settings_set_string(settings, FreeRDP_##key, value)) goto bad
    SET_STR(ServerHostname, host);
    SET_U32(ServerPort, port);
    SET_STR(Username, username);
    SET_STR(Domain, domain ? domain : "");
    SET_STR(Password, password);
    SET_U32(DesktopWidth, width);
    SET_U32(DesktopHeight, height);
    SET_U32(ColorDepth, 32);
    SET_BOOL(ExternalCertificateManagement, TRUE);
    SET_BOOL(CertificateCallbackPreferPEM, TRUE);
    SET_BOOL(IgnoreCertificate, FALSE);
    SET_BOOL(AutoAcceptCertificate, FALSE);
    SET_BOOL(AutoDenyCertificate, FALSE);
    SET_BOOL(NlaSecurity, TRUE);
    SET_BOOL(TlsSecurity, FALSE); /* No TLS-only fallback; NLA itself uses TLS. */
    SET_BOOL(RdpSecurity, FALSE);
    SET_BOOL(ExtSecurity, FALSE);
    SET_BOOL(NegotiateSecurityLayer, TRUE);
    SET_BOOL(Authentication, TRUE);
    SET_BOOL(AutoReconnectionEnabled, FALSE);
    SET_BOOL(RedirectClipboard, FALSE);
    SET_BOOL(RedirectDrives, FALSE);
    SET_BOOL(DeviceRedirection, FALSE);
    SET_BOOL(AudioPlayback, FALSE);
    SET_BOOL(AudioCapture, FALSE);
    SET_BOOL(RemoteConsoleAudio, FALSE);
    SET_BOOL(NetworkAutoDetect, FALSE);
    SET_BOOL(SupportHeartbeatPdu, FALSE);
    SET_BOOL(SupportMultitransport, FALSE);
    SET_BOOL(SupportGraphicsPipeline, TRUE);
    SET_BOOL(GfxSmallCache, TRUE);
    SET_BOOL(GfxThinClient, TRUE);
    SET_BOOL(SupportDynamicChannels, TRUE);
    SET_BOOL(SupportDisplayControl, TRUE);
    SET_BOOL(DynamicResolutionUpdate, TRUE);
#ifdef WITH_GFX_H264
    SET_BOOL(GfxH264, TRUE);
    SET_BOOL(GfxAVC444, TRUE);
    SET_BOOL(GfxAVC444v2, TRUE);
#else
    SET_BOOL(GfxH264, FALSE);
    SET_BOOL(GfxAVC444, FALSE);
    SET_BOOL(GfxAVC444v2, FALSE);
#endif
    SET_BOOL(DeactivateClientDecoding, FALSE);
    SET_BOOL(AsyncUpdate, FALSE);
    SET_BOOL(AsyncChannels, FALSE);
    SET_BOOL(TransportDump, FALSE);
    SET_BOOL(BitmapCachePersistEnabled, FALSE);
    SET_BOOL(LogonNotify, TRUE);
    SET_U32(TcpConnectTimeout, 10000);
#undef SET_BOOL
#undef SET_U32
#undef SET_STR
    return s;
bad:
    dh_rdp_free(s);
    return NULL;
}

int dh_rdp_run(void* session) {
    dh_session* s = session;
    if (!s) return -1;
    bool expected = false;
    if (!atomic_compare_exchange_strong(&s->used, &expected, true)) return -1;
    atomic_store(&s->active, true);
    bool connected = false;
    if (atomic_load(&s->stopping)) goto done;
    atomic_store(&s->state, DH_RDP_CONNECTING);
    connected = freerdp_connect(s->context->instance);
    clear_password(s);
    if (!connected) {
        if (!atomic_load(&s->stopping)) {
            uint32_t error = freerdp_get_last_error(s->context);
            fail(s, error ? error : DH_RDP_ERROR_CONNECT);
        }
        goto disconnect;
    }
    atomic_store(&s->state, DH_RDP_CONNECTED);
    while (!atomic_load(&s->stopping) && !freerdp_shall_disconnect_context(s->context)) {
        if (process_resize(s) != CHANNEL_RC_OK) {
            fail(s, DH_RDP_ERROR_EVENT);
            break;
        }
        HANDLE handles[MAXIMUM_WAIT_OBJECTS];
        const DWORD count = freerdp_get_event_handles(s->context, handles, MAXIMUM_WAIT_OBJECTS);
        if (!count || WaitForMultipleObjects(count, handles, FALSE, 100) == WAIT_FAILED) {
            fail(s, DH_RDP_ERROR_EVENT);
            break;
        }
        if (atomic_load(&s->stopping)) break;
        if (!freerdp_check_event_handles(s->context)) {
            const uint32_t error = freerdp_get_last_error(s->context);
            fail(s, error ? error : DH_RDP_ERROR_EVENT);
            break;
        }
    }
    if (!atomic_load(&s->stopping)) {
        const uint32_t error = freerdp_get_last_error(s->context);
        if (error) fail(s, error);
    }
disconnect:
    (void)freerdp_disconnect(s->context->instance);
done:
    clear_password(s);
    /* An explicit stop is a normal exit, even when the abort wakes a failed I/O. */
    if (atomic_load(&s->stopping)) atomic_store(&s->error, 0);
    const bool failed = atomic_load(&s->error) != 0;
    atomic_store(&s->state, failed ? DH_RDP_FAILED : DH_RDP_STOPPED);
    atomic_store(&s->active, false);
    return failed ? -1 : 0;
}

void dh_rdp_stop(void* session) {
    dh_session* s = session;
    if (!s) return;
    atomic_store(&s->stopping, true);
    int idle = DH_RDP_IDLE;
    (void)atomic_compare_exchange_strong(&s->state, &idle, DH_RDP_STOPPED);
    if (s->context) (void)freerdp_abort_connect_context(s->context);
}

void dh_rdp_free(void* session) {
    dh_session* s = session;
    if (!s || atomic_load(&s->active)) return;
    if (s->context) {
        clear_password(s);
        freerdp_client_context_free(s->context);
    }
    free(s->frame);
    pthread_mutex_destroy(&s->resize_lock);
    pthread_mutex_destroy(&s->frame_lock);
    OPENSSL_cleanse(s, sizeof(*s));
    free(s);
}

int dh_rdp_state(void* session) {
    return session ? atomic_load(&((dh_session*)session)->state) : DH_RDP_FAILED;
}
uint32_t dh_rdp_error(void* session) {
    return session ? atomic_load(&((dh_session*)session)->error) : DH_RDP_ERROR_ARGUMENT;
}
uint32_t dh_rdp_session_id(void* session) {
    return session ? atomic_load(&((dh_session*)session)->session_id) : UINT32_MAX;
}
int dh_rdp_copy_frame(void* session, uint8_t* destination, size_t capacity,
                      uint32_t* width, uint32_t* height, uint32_t* stride, uint64_t* sequence) {
    if (!session || !width || !height || !stride || !sequence) return -1;
    dh_session* s = session;
    pthread_mutex_lock(&s->frame_lock);
    *width = s->width; *height = s->height; *stride = s->stride; *sequence = s->sequence;
    int result = 0;
    if (s->frame_bytes) {
        if (!destination || capacity < s->frame_bytes) result = -1;
        else { memcpy(destination, s->frame, s->frame_bytes); result = 1; }
    }
    pthread_mutex_unlock(&s->frame_lock);
    return result;
}
