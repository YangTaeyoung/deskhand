/* Compile the actual implementation into this test so private callbacks can be
 * driven without exporting test-only ABI or requiring a Windows password. */
#include <dns_sd.h>
static DNSServiceErrorType mock_dns_start(DNSServiceRef*, DNSServiceFlags, uint32_t,
    DNSServiceProtocol, const char*, DNSServiceGetAddrInfoReply, void*);
static int mock_dns_fd(DNSServiceRef);
static DNSServiceErrorType mock_dns_process(DNSServiceRef);
static void mock_dns_free(DNSServiceRef);
#define DNSServiceGetAddrInfo mock_dns_start
#define DNSServiceRefSockFD mock_dns_fd
#define DNSServiceProcessResult mock_dns_process
#define DNSServiceRefDeallocate mock_dns_free
#include "../deskhand_rdp.c"
#undef DNSServiceGetAddrInfo
#undef DNSServiceRefSockFD
#undef DNSServiceProcessResult
#undef DNSServiceRefDeallocate
#include <assert.h>
#include <arpa/inet.h>
#include <poll.h>
#include <stdio.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>
#include <openssl/ssl.h>
#include <openssl/x509v3.h>
#include <freerdp/codec/h264.h>

static const char* dummy_pin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

static void* run_session(void* arg) {
    int result = dh_rdp_run(arg);
    return (void*)(intptr_t)result;
}

static double now(void) {
    struct timespec ts;
    assert(clock_gettime(CLOCK_MONOTONIC, &ts) == 0);
    return ts.tv_sec + ts.tv_nsec / 1e9;
}

/* A pipe-backed DNS-SD injector exercises the real poll/cancel lifecycle. */
static struct {
    bool enabled, delayed, active;
    int pipefd[2], frees, attempts;
    DNSServiceGetAddrInfoReply reply;
    void* context;
    pthread_t producer;
    atomic_bool a_processed, attempting;
} dns_mock;

static void* delayed_a(void* unused) {
    (void)unused;
    usleep(60000);
    assert(write(dns_mock.pipefd[1], "4", 1) == 1);
    return NULL;
}

static DNSServiceErrorType mock_dns_start(DNSServiceRef* ref, DNSServiceFlags flags,
    uint32_t index, DNSServiceProtocol protocol, const char* host,
    DNSServiceGetAddrInfoReply reply, void* context) {
    if (!dns_mock.enabled)
        return DNSServiceGetAddrInfo(ref, flags, index, protocol, host, reply, context);
    assert(pipe(dns_mock.pipefd) == 0);
    dns_mock.active = true;
    dns_mock.reply = reply;
    dns_mock.context = context;
    *ref = (DNSServiceRef)&dns_mock;
    if (dns_mock.delayed) {
        assert(write(dns_mock.pipefd[1], "6", 1) == 1);
        assert(pthread_create(&dns_mock.producer, NULL, delayed_a, NULL) == 0);
    }
    return kDNSServiceErr_NoError;
}

static int mock_dns_fd(DNSServiceRef ref) {
    return ref == (DNSServiceRef)&dns_mock ? dns_mock.pipefd[0] : DNSServiceRefSockFD(ref);
}

static DNSServiceErrorType mock_dns_process(DNSServiceRef ref) {
    if (ref != (DNSServiceRef)&dns_mock) return DNSServiceProcessResult(ref);
    char family;
    assert(read(dns_mock.pipefd[0], &family, 1) == 1);
    struct sockaddr_in6 v6 = {.sin6_family = AF_INET6};
    struct sockaddr_in v4 = {.sin_family = AF_INET};
    assert(inet_pton(AF_INET6, "2001:db8::1", &v6.sin6_addr) == 1);
    assert(inet_pton(AF_INET, "192.0.2.1", &v4.sin_addr) == 1);
    dns_mock.reply(ref, kDNSServiceFlagsAdd, 0, 0, "fixture.invalid",
        family == '6' ? (struct sockaddr*)&v6 : (struct sockaddr*)&v4, 60, dns_mock.context);
    if (family == '4') atomic_store(&dns_mock.a_processed, true);
    return kDNSServiceErr_NoError;
}

static void mock_dns_free(DNSServiceRef ref) {
    if (ref != (DNSServiceRef)&dns_mock) { DNSServiceRefDeallocate(ref); return; }
    if (dns_mock.delayed) assert(pthread_join(dns_mock.producer, NULL) == 0);
    close(dns_mock.pipefd[0]);
    close(dns_mock.pipefd[1]);
    dns_mock.active = false;
    ++dns_mock.frees;
}

static int mock_numeric_connect(rdpContext* ctx, rdpSettings* settings,
    const char* host, int port, DWORD timeout) {
    (void)settings; (void)port;
    ++dns_mock.attempts;
    if (!strcmp(host, "192.168.88.165")) { assert(timeout == 789); return 123; }
    assert(dns_mock.active); /* Lookup must survive each numeric attempt. */
    if (!strcmp(host, "192.0.2.1")) {
        assert(atomic_load(&dns_mock.a_processed));
        return 123;
    }
    assert(!strcmp(host, "2001:db8::1") && timeout > 0 && timeout <= 250);
    atomic_store(&dns_mock.attempting, true);
    double start = now();
    while (!atomic_load(&owner(ctx)->stopping) && (now() - start) * 1000 < timeout)
        usleep(1000);
    if (!atomic_load(&owner(ctx)->stopping))
        assert(atomic_load(&dns_mock.a_processed)); /* Delivered DURING the attempt. */
    freerdp_set_last_error(ctx, FREERDP_ERROR_CONNECT_FAILED);
    return -1;
}

static void* run_dns(void* arg) {
    dh_session* s = arg;
    return (void*)(intptr_t)tcp_connect(s->context, s->context->settings, "fixture.invalid", 3389, 2000);
}

static void test_dns(void) {
    dns_mock.enabled = true;
    dns_mock.delayed = true;
    atomic_init(&dns_mock.a_processed, false);
    atomic_init(&dns_mock.attempting, false);
    dh_session* s = dh_rdp_new("fixture.invalid", 3389, "test", "", "", dummy_pin, 256, 256);
    assert(s);
    s->tcp_connect = mock_numeric_connect;
    double start = now();
    assert(tcp_connect(s->context, s->context->settings, "fixture.invalid", 3389, 800) == 123);
    assert(now() - start < 0.8 && dns_mock.attempts == 2 && dns_mock.frees == 1);
    assert(!freerdp_get_last_error(s->context));
    assert(!dns_mock.active);
    assert(tcp_connect(s->context, s->context->settings, "192.168.88.165", 3389, 789) == 123);
    assert(dns_mock.frees == 1); /* Numeric target bypasses DNS and preserves timeout. */
    dh_rdp_free(s);

    dns_mock.delayed = false;
    s = dh_rdp_new("fixture.invalid", 3389, "test", "", "", dummy_pin, 256, 256);
    assert(s);
    s->tcp_connect = mock_numeric_connect;
    start = now();
    assert(tcp_connect(s->context, s->context->settings, "fixture.invalid", 3389, 120) == -1);
    assert(now() - start >= 0.10 && now() - start < 0.5);
    assert(dns_mock.frees == 2 && !dns_mock.active && dh_rdp_error(s) == DH_RDP_ERROR_DNS);
    dh_rdp_free(s);
    s = dh_rdp_new("fixture.invalid", 3389, "test", "", "", dummy_pin, 256, 256);
    assert(s);
    pthread_t worker;
    assert(pthread_create(&worker, NULL, run_dns, s) == 0);
    usleep(60000);
    start = now();
    dh_rdp_stop(s);
    void* result;
    assert(pthread_join(worker, &result) == 0 && (intptr_t)result == -1);
    assert(now() - start < 0.3 && dns_mock.frees == 3 && !dns_mock.active);
    assert(!dh_rdp_error(s));
    dh_rdp_free(s);

    dns_mock.delayed = true;
    atomic_store(&dns_mock.a_processed, false);
    atomic_store(&dns_mock.attempting, false);
    s = dh_rdp_new("fixture.invalid", 3389, "test", "", "", dummy_pin, 256, 256);
    assert(s);
    s->tcp_connect = mock_numeric_connect;
    assert(pthread_create(&worker, NULL, run_dns, s) == 0);
    start = now();
    while (!atomic_load(&dns_mock.attempting)) { assert(now() - start < 1); usleep(1000); }
    start = now();
    dh_rdp_stop(s); /* Also interrupt an already active numeric attempt. */
    assert(pthread_join(worker, &result) == 0 && (intptr_t)result == -1);
    assert(now() - start < 0.3 && dns_mock.frees == 4 && !dns_mock.active);
    assert(!dh_rdp_error(s));
    dh_rdp_free(s);
    dns_mock.enabled = false;
    puts("DNS delayed A after AAAA during bounded attempt, total timeout, stop, numeric bypass: PASS");
}

static unsigned creates, deletes;
static bool live_surfaces[UINT16_MAX + 1], fail_create, fail_delete;
static UINT fake_create(RdpgfxClientContext* gfx, const RDPGFX_CREATE_SURFACE_PDU* pdu) {
    (void)gfx;
    ++creates;
    if (fail_create) return ERROR_NOT_ENOUGH_MEMORY;
    assert(!live_surfaces[pdu->surfaceId]);
    live_surfaces[pdu->surfaceId] = true;
    return CHANNEL_RC_OK;
}

static UINT fake_delete(RdpgfxClientContext* gfx, const RDPGFX_DELETE_SURFACE_PDU* pdu) {
    (void)gfx;
    ++deletes;
    if (fail_delete) return ERROR_INTERNAL_ERROR;
    live_surfaces[pdu->surfaceId] = false;
    return CHANNEL_RC_OK;
}

static void test_surface_budget(void) {
    dh_session* s = dh_rdp_new("127.0.0.1", 3389, "test", "", "", dummy_pin, 256, 256);
    assert(s);
    rdpGdi gdi = {.context = s->context};
    RdpgfxClientContext gfx = {.custom = &gdi};
    s->gfx_create_surface = fake_create;
    s->gfx_delete_surface = fake_delete;
    s->gfx = &gfx;
    gfx.CreateSurface = create_surface;
    gfx.DeleteSurface = delete_surface;
    RDPGFX_CREATE_SURFACE_PDU pdu = {.width = 1920, .height = 1080};
    for (unsigned i = 0; i < 16; ++i) {
        pdu.surfaceId = i;
        assert(create_surface(&gfx, &pdu) == CHANNEL_RC_OK);
    }
    assert(creates == 16);
    pdu.surfaceId = 16;
    assert(create_surface(&gfx, &pdu) == ERROR_INVALID_DATA && creates == 16 && deletes == 0);
    pdu.surfaceId = 0;
    assert(create_surface(&gfx, &pdu) == ERROR_INVALID_DATA && creates == 16 && deletes == 0);
    assert(s->surface_count == 16 && s->surface_bytes == (size_t)16 * (1920 * 4 + 16) * 1088);
    RDPGFX_DELETE_SURFACE_PDU remove = {.surfaceId = 0};
    fail_delete = true;
    assert(delete_surface(&gfx, &remove) == ERROR_INTERNAL_ERROR && s->surface_count == 16);
    fail_delete = false;
    assert(delete_surface(&gfx, &remove) == CHANNEL_RC_OK && s->surface_count == 15);
    /* This is the pinned channel's replacement sequence: delete then create. */
    pdu.width = 1280;
    pdu.height = 720;
    fail_create = true;
    size_t retained = s->surface_bytes;
    assert(create_surface(&gfx, &pdu) == ERROR_NOT_ENOUGH_MEMORY);
    assert(s->surface_count == 15 && s->surface_bytes == retained);
    fail_create = false;
    assert(create_surface(&gfx, &pdu) == CHANNEL_RC_OK && s->surface_count == 16);
    assert(s->surface_bytes == retained + (1280 * 4 + 16) * 720);
    release_surfaces(s);
    assert(s->surface_bytes == 0 && s->surface_count == 0 && !s->gfx);
    assert(gfx.CreateSurface == fake_create && gfx.DeleteSurface == fake_delete);
    for (size_t i = 0; i <= UINT16_MAX; ++i) assert(!live_surfaces[i]);

    s->gfx = &gfx;
    pdu = (RDPGFX_CREATE_SURFACE_PDU){.surfaceId = 42, .width = 8192, .height = 4096};
    assert(create_surface(&gfx, &pdu) == CHANNEL_RC_OK);
    assert(s->surface_bytes == (size_t)(8192 * 4 + 16) * 4096);
    unsigned before_create = creates, before_delete = deletes;
    pdu.surfaceId = 43;
    /* Two raw 128 MiB frames fit; their FreeRDP padding exceeds the budget. */
    assert(create_surface(&gfx, &pdu) == ERROR_INVALID_DATA);
    assert(creates == before_create && deletes == before_delete && s->surface_count == 1);
    remove.surfaceId = 42;
    assert(delete_surface(&gfx, &remove) == CHANNEL_RC_OK && s->surface_bytes == 0);
    assert(create_surface(&gfx, &pdu) == CHANNEL_RC_OK); /* Budget restored. */
    release_surfaces(s);
    assert(s->surface_bytes == 0 && s->surface_count == 0 && !live_surfaces[43]);
    before_create = creates;
    pdu.width = pdu.height = 8192;
    assert(create_surface(&gfx, &pdu) == ERROR_INVALID_DATA && creates == before_create);
    dh_rdp_free(s);
    puts("GFX count/aggregate padded bytes, duplicate rejection, replacement, failure rollback, disconnect: PASS");
}

static int listen_local(uint16_t* port) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    assert(fd >= 0);
    struct sockaddr_in addr = {.sin_len = sizeof(addr), .sin_family = AF_INET,
        .sin_addr.s_addr = htonl(INADDR_LOOPBACK)};
    assert(bind(fd, (struct sockaddr*)&addr, sizeof(addr)) == 0);
    assert(listen(fd, 1) == 0);
    socklen_t len = sizeof(addr);
    assert(getsockname(fd, (struct sockaddr*)&addr, &len) == 0);
    *port = ntohs(addr.sin_port);
    return fd;
}

static int accept_local(int listener) {
    struct pollfd p = {.fd = listener, .events = POLLIN};
    assert(poll(&p, 1, 3000) == 1);
    int fd = accept(listener, NULL, NULL);
    assert(fd >= 0);
    struct timeval timeout = {.tv_sec = 3};
    assert(setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) == 0);
    assert(setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout)) == 0);
    int one = 1;
    assert(setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, sizeof(one)) == 0);
    return fd;
}

static void read_request(int fd) {
    uint8_t buffer[65536];
    assert(recv(fd, buffer, 4, MSG_WAITALL) == 4);
    assert(buffer[0] == 3 && buffer[1] == 0);
    size_t size = ((size_t)buffer[2] << 8) | buffer[3];
    assert(size > 4 && size <= sizeof(buffer));
    assert(recv(fd, buffer + 4, size - 4, MSG_WAITALL) == (ssize_t)size - 4);
}

static void test_stop_connect(void) {
    for (int i = 0; i < 8; i++) {
        uint16_t port;
        int listener = listen_local(&port);
        dh_session* s = dh_rdp_new(i % 2 ? "localhost" : "127.0.0.1", port,
                                    "test", "", "", dummy_pin, 256, 256);
        assert(s);
        pthread_t worker;
        assert(pthread_create(&worker, NULL, run_session, s) == 0);
        int fd = accept_local(listener);
        read_request(fd); /* Client really is blocked waiting for RDP negotiation. */
        assert(dh_rdp_state(s) == DH_RDP_CONNECTING);
        assert(dh_rdp_run(s) != 0); /* A second run must not mutate the first. */
        dh_rdp_free(s); /* Active free is ignored. */
        assert(dh_rdp_state(s) == DH_RDP_CONNECTING);
        double start = now();
        dh_rdp_stop(s);
        void* result;
        assert(pthread_join(worker, &result) == 0 && (intptr_t)result == 0);
        assert(now() - start < 1.0);
        assert(dh_rdp_state(s) == DH_RDP_STOPPED && dh_rdp_error(s) == 0);
        assert(!freerdp_settings_get_string(s->context->settings, FreeRDP_Password));
        dh_rdp_free(s);
        close(fd);
        close(listener);
    }
    puts("Stop during real blocked connect, active free, once-only run: PASS");
}

static void test_no_tls_fallback(void) {
    uint16_t port;
    int listener = listen_local(&port);
    dh_session* s = dh_rdp_new("127.0.0.1", port, "test", "", "", dummy_pin, 256, 256);
    pthread_t worker;
    assert(s && pthread_create(&worker, NULL, run_session, s) == 0);
    int fd = accept_local(listener);
    read_request(fd);
    const uint8_t response[] = {3,0,0,19,14,0xd0,0,0,0,0,0,2,0,8,0,1,0,0,0};
    assert(send(fd, response, sizeof(response), 0) == sizeof(response));
    uint8_t unexpected[16];
    assert(recv(fd, unexpected, sizeof(unexpected), 0) <= 0);
    close(fd);
    close(listener);
    void* result;
    assert(pthread_join(worker, &result) == 0 && (intptr_t)result != 0);
    assert(dh_rdp_state(s) == DH_RDP_FAILED && dh_rdp_error(s));
    dh_rdp_free(s);
    puts("TLS-only server rejected: NLA downgrade blocked: PASS");
}

typedef struct { dh_session* session; atomic_bool done; } frame_reader;
static void* read_frames(void* arg) {
    frame_reader* reader = arg;
    uint64_t last = 0;
    do {
        uint32_t w, h, stride;
        uint64_t seq;
        uint8_t pixels[16];
        int rc = dh_rdp_copy_frame(reader->session, pixels, sizeof(pixels), &w, &h, &stride, &seq);
        assert(rc == 1 && w >= 1 && w <= 2 && h == 2 && stride == w * 4 && seq >= last);
        for (size_t i = 3; i < (size_t)stride * h; i += 4) assert(pixels[i] == 255);
        last = seq;
    } while (!atomic_load(&reader->done));
    return NULL;
}

static UINT surface_called(RdpgfxClientContext* gfx, const RDPGFX_CREATE_SURFACE_PDU* pdu) {
    (void)gfx; (void)pdu;
    return 777;
}

static void test_h264(void) {
#ifdef WITH_VIDEO_FFMPEG
    /* Red 16x16 IDR, generated by ffmpeg lavfi color -> libx264 ultrafast
     * zerolatency, with the optional encoder SEI NAL removed. No external tool
     * or fixture download is needed to run this decoder regression test. */
    static const uint8_t idr[] = {
        0x00,0x00,0x00,0x01,0x67,0x42,0xc0,0x0a,0xda,0x7b,0x01,0x10,
        0x00,0x00,0x03,0x00,0x10,0x00,0x00,0x03,0x00,0x28,0xf1,0x22,
        0x6a,0x00,0x00,0x00,0x01,0x68,0xce,0x0f,0xc8,0x00,0x00,0x00,
        0x01,0x65,0x88,0x84,0x3a,0x11,0x8a,0x00,0x02,0x18,0xf1,0xc0,
        0x00,0x40,0xf6,0x38,0x00,0x08,0x79,0x60
    };
    H264_CONTEXT* decoder = h264_context_new(FALSE);
    assert(decoder && h264_context_reset(decoder, 16, 16));
    uint8_t pixels[16 * 16 * 4] = {0};
    RECTANGLE_16 region = {.left = 0, .top = 0, .right = 16, .bottom = 16};
    assert(avc420_decompress(decoder, idr, sizeof(idr), pixels, PIXEL_FORMAT_BGRA32,
                             16 * 4, 16, 16, &region, 1) > 0);
    for (size_t i = 0; i < sizeof(pixels); i += 4)
        assert(pixels[i] < 50 && pixels[i + 1] < 50 && pixels[i + 2] > 200);
    h264_context_free(decoder);
    puts("FreeRDP FFmpeg H.264 real IDR decode to BGRA: PASS");
#else
    puts("FFmpeg H.264 unavailable: GDI/GFX fallback only");
#endif
}

static void extension(X509* cert, int nid, const char* value) {
    X509V3_CTX ctx;
    X509V3_set_ctx(&ctx, cert, cert, NULL, NULL, 0);
    X509_EXTENSION* ext = X509V3_EXT_conf_nid(NULL, &ctx, nid, value);
    assert(ext && X509_add_ext(cert, ext, -1));
    X509_EXTENSION_free(ext);
}

static X509* certificate(EVP_PKEY* key) {
    X509* cert = X509_new();
    assert(cert && X509_set_version(cert, 2));
    assert(ASN1_INTEGER_set(X509_get_serialNumber(cert), 1));
    assert(X509_gmtime_adj(X509_getm_notBefore(cert), -60));
    assert(X509_gmtime_adj(X509_getm_notAfter(cert), 3600));
    assert(X509_set_pubkey(cert, key));
    X509_NAME* name = X509_get_subject_name(cert);
    assert(X509_NAME_add_entry_by_txt(name, "CN", MBSTRING_ASC,
                                     (const unsigned char*)"localhost", -1, -1, 0));
    assert(X509_set_issuer_name(cert, name));
    extension(cert, NID_basic_constraints, "critical,CA:TRUE");
    extension(cert, NID_key_usage, "critical,digitalSignature,keyCertSign");
    extension(cert, NID_subject_alt_name, "DNS:localhost,IP:127.0.0.1");
    assert(X509_sign(cert, key, EVP_sha256()));
    return cert;
}

static void test_certificate(void) {
    EVP_PKEY* key = EVP_PKEY_Q_keygen(NULL, NULL, "EC", "prime256v1");
    assert(key);
    X509* cert = certificate(key);
    unsigned char digest[32];
    unsigned int size;
    assert(X509_digest(cert, EVP_sha256(), digest, &size) && size == 32);
    char pin[65];
    for (size_t i = 0; i < 32; i++) snprintf(pin + i * 2, 3, "%02x", digest[i]);
    BIO* pem = BIO_new(BIO_s_mem());
    assert(pem && PEM_write_bio_X509(pem, cert));
    char* text;
    size_t length = (size_t)BIO_get_mem_data(pem, &text);
    dh_session* s = dh_rdp_new("127.0.0.1", 3389, "test", "", "", pin, 256, 256);
    assert(s);
    assert(freerdp_settings_get_bool(s->context->settings, FreeRDP_ExternalCertificateManagement));
    assert(freerdp_settings_get_bool(s->context->settings, FreeRDP_NlaSecurity));
    assert(!freerdp_settings_get_bool(s->context->settings, FreeRDP_TlsSecurity));
    assert(!freerdp_settings_get_bool(s->context->settings, FreeRDP_IgnoreCertificate));
    assert(freerdp_settings_get_bool(s->context->settings, FreeRDP_GfxH264) ==
           (bool)strstr(freerdp_get_build_config(), "WITH_VIDEO_FFMPEG=ON"));
    assert(verify_certificate(s->context->instance, (BYTE*)text, length, "localhost", 3389, 0) == 2);
    assert(verify_certificate(s->context->instance, (BYTE*)"bad", 3, "localhost", 3389, 0) == 0);
    dh_rdp_free(s);

    /* Make this certificate PKI-trusted for this test process only. Even for a
     * trusted, name-matching leaf, the real FreeRDP TLS path MUST enforce pin. */
    FILE* trust = fopen("test-trust.pem", "w");
    assert(trust && PEM_write_X509(trust, cert));
    assert(fclose(trust) == 0);
    assert(setenv("SSL_CERT_FILE", "test-trust.pem", 1) == 0);
    X509_STORE* store = X509_STORE_new();
    X509_STORE_CTX* verify = X509_STORE_CTX_new();
    assert(store && verify && X509_STORE_set_default_paths(store));
    assert(X509_STORE_CTX_init(verify, store, cert, NULL));
    assert(X509_verify_cert(verify) == 1);
    X509_STORE_CTX_free(verify);
    X509_STORE_free(store);
    SSL_CTX* tls = SSL_CTX_new(TLS_server_method());
    assert(tls && SSL_CTX_use_certificate(tls, cert) && SSL_CTX_use_PrivateKey(tls, key));
    for (int matching = 0; matching < 2; matching++) {
        uint16_t port;
        int listener = listen_local(&port);
        s = dh_rdp_new("127.0.0.1", port, "test", "", "", matching ? pin : dummy_pin, 256, 256);
        pthread_t worker;
        assert(s && pthread_create(&worker, NULL, run_session, s) == 0);
        int fd = accept_local(listener);
        read_request(fd);
        const uint8_t response[] = {3,0,0,19,14,0xd0,0,0,0,0,0,2,0,8,0,2,0,0,0};
        assert(send(fd, response, sizeof(response), 0) == sizeof(response));
        SSL* ssl = SSL_new(tls);
        assert(ssl && SSL_set_fd(ssl, fd));
        assert(SSL_accept(ssl) == 1);
        uint8_t credssp[4096];
        const int got = SSL_read(ssl, credssp, sizeof(credssp));
        if (matching) assert(got > 0); /* Pin accepted, NLA exchange began. */
        else assert(got <= 0); /* Trusted TLS certificate rejected before NLA. */
        SSL_free(ssl);
        close(fd);
        close(listener);
        void* result;
        assert(pthread_join(worker, &result) == 0 && (intptr_t)result != 0);
        assert(dh_rdp_state(s) == DH_RDP_FAILED);
        if (matching) assert(dh_rdp_error(s) != DH_RDP_ERROR_CERTIFICATE);
        else assert(dh_rdp_error(s) == DH_RDP_ERROR_CERTIFICATE);
        dh_rdp_free(s);
    }
    SSL_CTX_free(tls);
    BIO_free(pem);
    X509_free(cert);
    EVP_PKEY_free(key);
    assert(unlink("test-trust.pem") == 0);
    puts("Certificate DER SHA-256 and real TLS pin rejection even when PKI-trusted: PASS");
}

static void test_frame_and_session(void) {
    dh_session* s = dh_rdp_new("127.0.0.1", 3389, "test", "", "", dummy_pin, 256, 256);
    assert(s);
    rdpContext* ctx = s->context;
    logon_info_ex info = {.haveCookie = FALSE, .LogonId = 1234};
    assert(save_session_info(ctx, INFO_TYPE_LOGON_EXTENDED_INF, &info));
    assert(dh_rdp_session_id(s) == UINT32_MAX);
    logon_info v1 = {.sessionId = 4321};
    assert(save_session_info(ctx, INFO_TYPE_LOGON, &v1));
    assert(dh_rdp_session_id(s) == 4321);
    logon_info v2 = {.sessionId = 8765};
    assert(save_session_info(ctx, INFO_TYPE_LOGON_LONG, &v2));
    assert(dh_rdp_session_id(s) == 8765);
    info.haveCookie = TRUE;
    assert(save_session_info(ctx, INFO_TYPE_LOGON_EXTENDED_INF, &info));
    assert(dh_rdp_session_id(s) == 1234);
    uint8_t pixels[] = {1,2,3,0,4,5,6,7,0xaa,0xaa,0xaa,0xaa,
                        8,9,10,11,12,13,14,15,0xbb,0xbb,0xbb,0xbb};
    GDI_RGN invalid = {.null = TRUE};
    GDI_WND window = {.invalid = &invalid};
    GDI_DC dc = {.hwnd = &window};
    gdiBitmap primary = {.hdc = &dc};
    rdpGdi gdi = {.context = ctx, .width = 2, .height = 2, .stride = 12,
                   .primary_buffer = pixels, .primary = &primary};
    ctx->gdi = &gdi;
    assert(end_paint(ctx) && s->sequence == 0); /* No changed pixels. */
    invalid.null = FALSE;
    assert(end_paint(ctx) && s->sequence == 1);
    uint32_t w, h, stride;
    uint64_t seq;
    assert(dh_rdp_copy_frame(s, NULL, 0, &w, &h, &stride, &seq) == -1);
    assert(w == 2 && h == 2 && stride == 8 && seq == 1);
    uint8_t out[20]; memset(out, 0xee, sizeof(out));
    assert(dh_rdp_copy_frame(s, out, 15, &w, &h, &stride, &seq) == -1);
    assert(out[0] == 0xee && out[19] == 0xee);
    assert(dh_rdp_copy_frame(s, out, 16, &w, &h, &stride, &seq) == 1);
    assert(out[0] == 1 && out[4] == 4 && out[8] == 8 && out[12] == 12 && out[16] == 0xee);
    for (int i = 3; i < 16; i += 4) assert(out[i] == 255);
    assert(dh_rdp_copy_frame(s, out, 16, &w, &h, &stride, &seq) == 1 && seq == 1);
    assert(begin_paint(ctx) && end_paint(ctx) && s->sequence == 1);
    frame_reader reader = {.session = s};
    atomic_init(&reader.done, false);
    pthread_t worker;
    assert(pthread_create(&worker, NULL, read_frames, &reader) == 0);
    for (int i = 0; i < 1000; i++) {
        gdi.width = i % 2 ? 1 : 2;
        invalid.null = FALSE;
        assert(end_paint(ctx));
        assert(s->frame_bytes == (size_t)gdi.width * 2 * 4);
    }
    atomic_store(&reader.done, true);
    assert(pthread_join(worker, NULL) == 0);
    RdpgfxClientContext gfx = {.custom = &gdi};
    s->gfx_create_surface = surface_called;
    RDPGFX_CREATE_SURFACE_PDU surface = {.width = 8192, .height = 4096};
    assert(create_surface(&gfx, &surface) == 777);
    surface.width = 8193;
    assert(create_surface(&gfx, &surface) == ERROR_INVALID_DATA);
    size_t bytes;
    assert(frame_size(8192, 8192, &bytes) && bytes == DH_RDP_MAX_FRAME_BYTES);
    assert(!frame_size(8193, 8192, &bytes));
    gdi.width = 8193;
    assert(!end_paint(ctx) && dh_rdp_error(s) == DH_RDP_ERROR_FRAME_SIZE);
    assert(freerdp_settings_set_uint32(ctx->settings, FreeRDP_DesktopWidth, 8193));
    assert(!desktop_resize(ctx)); /* Reject before any GDI allocation. */
    ctx->gdi = NULL; /* Local fixture does not belong to FreeRDP. */
    dh_rdp_free(s);
    puts("Frame BGRA/opaque alpha, stride/capacity, changed EndPaint, bounded resize, server LogonId: PASS");
}

static DISPLAY_CONTROL_MONITOR_LAYOUT sent_layout;
static int layout_calls;
static UINT capture_layout(DispClientContext* context, UINT32 count, DISPLAY_CONTROL_MONITOR_LAYOUT* layout) {
    (void)context; assert(count == 1); sent_layout = *layout; layout_calls++; return CHANNEL_RC_OK;
}
static void test_dynamic_resize(void) {
    dh_session* s = dh_rdp_new("localhost", 9, "test", "", "", dummy_pin, 800, 600);
    assert(s);
    assert(freerdp_settings_get_bool(s->context->settings, FreeRDP_SupportDisplayControl));
    assert(!dh_rdp_resize(s, 199, 600, 100));
    assert(!dh_rdp_resize(s, 800, 600, 501));
    assert(dh_rdp_resize(s, 1201, 800, 200));
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 0); /* Wait for caps. */
    DispClientContext disp = {.custom = s, .SendMonitorLayout = capture_layout};
    ChannelConnectedEventArgs connected = {.name = DISP_DVC_CHANNEL_NAME, .pInterface = &disp};
    channel_connected(s->context, &connected);
    assert(disp.DisplayControlCaps);
    assert(disp.DisplayControlCaps(&disp, 1, 4096, 4096) == CHANNEL_RC_OK);
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 1);
    assert(sent_layout.Width == 1200 && sent_layout.Height == 800);
    assert(sent_layout.DesktopScaleFactor == 200 && sent_layout.DeviceScaleFactor == 180);
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 1); /* No duplicate. */
    assert(dh_rdp_resize(s, 1600, 900, 100));
    assert(dh_rdp_resize(s, 1800, 1000, 100)); /* Only newest pending layout. */
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 2 && sent_layout.Width == 1800);
    assert(dh_rdp_resize(s, 8192, 8192, 200));
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 3); /* Fit server area. */
    assert((uint64_t)sent_layout.Width * sent_layout.Height <= 4096ULL * 4096);
    assert(disp.DisplayControlCaps(&disp, 4, 1920, 1080) == CHANNEL_RC_OK);
    assert(dh_rdp_resize(s, 2560, 1440, 100));
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 4 && sent_layout.Width == 2560);
    ChannelDisconnectedEventArgs disconnected = {.name = DISP_DVC_CHANNEL_NAME, .pInterface = &disp};
    channel_disconnected(s->context, &disconnected);
    assert(dh_rdp_resize(s, 800, 600, 100));
    assert(process_resize(s) == CHANNEL_RC_OK && layout_calls == 4);
    dh_rdp_stop(s);
    assert(!dh_rdp_resize(s, 800, 600, 100));
    dh_rdp_free(s);
    puts("Display caps, latest bounded resize, DPI, disconnect and stop: PASS");
}

int main(int argc, char** argv) {
    if (argc == 2 && !strcmp(argv[1], "--dns")) { test_dns(); return 0; }
    if (argc == 2 && !strcmp(argv[1], "--gfx")) { test_surface_budget(); return 0; }
    test_dynamic_resize();
    test_dns();
    test_surface_budget();
    test_stop_connect();
    test_no_tls_fallback();
    test_certificate();
    test_frame_and_session();
    test_h264();
    return 0;
}
