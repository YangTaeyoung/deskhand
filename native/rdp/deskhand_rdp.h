#ifndef DESKHAND_RDP_H
#define DESKHAND_RDP_H
#include <stddef.h>
#include <stdint.h>

#if defined(__GNUC__)
#define DH_RDP_API __attribute__((visibility("default")))
#else
#define DH_RDP_API
#endif
#ifdef __cplusplus
extern "C" {
#endif

enum { DH_RDP_IDLE, DH_RDP_CONNECTING, DH_RDP_CONNECTED, DH_RDP_STOPPED, DH_RDP_FAILED };
/* Engine errors occupy a range distinct from FreeRDP errors. Never contain credentials. */
enum {
    DH_RDP_ERROR_ARGUMENT = 0xD4000001u,
    DH_RDP_ERROR_ONCE = 0xD4000002u,
    DH_RDP_ERROR_MEMORY = 0xD4000003u,
    DH_RDP_ERROR_CERTIFICATE = 0xD4000004u,
    DH_RDP_ERROR_FRAME_SIZE = 0xD4000005u,
    DH_RDP_ERROR_EVENT = 0xD4000006u,
    DH_RDP_ERROR_CONNECT = 0xD4000007u,
    DH_RDP_ERROR_SECURITY = 0xD4000008u,
    DH_RDP_ERROR_DNS = 0xD4000009u
};
#define DH_RDP_MAX_DIMENSION 8192u
#define DH_RDP_MAX_FRAME_BYTES ((size_t)256 * 1024 * 1024)

/* Copies strings. Pin: exactly 64 hex characters, or 32 colon-separated hex pairs.
 * Initial width/height must be 200..8192 (FreeRDP's monitor minimum is 200).
 * Invalid config/allocation returns NULL. No configuration, password or pin is persisted.
 * Caller owns its password buffer and should erase it after this call. */
DH_RDP_API void* dh_rdp_new(const char* host, uint16_t port, const char* username,
                          const char* domain, const char* password,
                          const char* certificate_sha256, uint32_t width, uint32_t height);
/* Exactly one caller, exactly once per session. Blocking. Zero = normal stop.
 * A second invocation fails without changing an active/finished session's state. */
DH_RDP_API int dh_rdp_run(void* session);
/* Thread safe and idempotent, including before run. Does not free anything. */
/* Thread-safe latest-only display request. Returns 0 for invalid/stopped requests.
 * Queued until Display Control caps arrive; sent by the run loop, not the UI. */
DH_RDP_API int dh_rdp_resize(void* session, uint32_t width, uint32_t height, uint32_t desktop_scale);
DH_RDP_API void dh_rdp_stop(void* session);
/* Caller MUST join run and all other API callers before free. Active free is ignored. */
DH_RDP_API void dh_rdp_free(void* session);
DH_RDP_API int dh_rdp_state(void* session);
DH_RDP_API uint32_t dh_rdp_error(void* session);
/* Latest tightly packed BGRA32, alpha always 255, one owned snapshot, monotonically
 * increasing sequence. 0 = no frame, 1 = copied, -1 = too small/invalid outputs.
 * All output pointers are required. NULL destination/capacity 0 queries dimensions
 * and returns -1 when a frame exists. Dimensions/sequence are set before -1.
 * Caller may see the same frame repeatedly and should compare sequence. */
DH_RDP_API int dh_rdp_copy_frame(void* session, uint8_t* destination, size_t capacity,
                               uint32_t* width, uint32_t* height, uint32_t* stride,
                               uint64_t* sequence);
/* Server SaveSessionInfo V1/V2 sessionId or extended LogonId, UINT32_MAX if unknown.
 * Never the client's ClientSessionId. Unknown must keep agent input disabled. */
DH_RDP_API uint32_t dh_rdp_session_id(void* session);
#ifdef __cplusplus
}
#endif
#endif
