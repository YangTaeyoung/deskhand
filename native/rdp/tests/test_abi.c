#include "deskhand_rdp.h"
#include <assert.h>
#include <pthread.h>
#include <stdio.h>
#include <time.h>

static const char* pin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
static void* make(void) {
    return dh_rdp_new("127.0.0.1", 9, "test", "", "", pin, 256, 256);
}
static void* run(void* session) {
    (void)dh_rdp_run(session);
    return NULL;
}
int main(void) {
    assert(!dh_rdp_new(NULL, 3389, "u", "", "", pin, 256, 256));
    assert(!dh_rdp_new("", 3389, "u", "", "", pin, 256, 256));
    assert(!dh_rdp_new("h", 0, "u", "", "", pin, 256, 256));
    assert(!dh_rdp_new("h", 3389, "", "", "", pin, 256, 256));
    assert(!dh_rdp_new("h", 3389, "u", "", NULL, pin, 256, 256));
    assert(!dh_rdp_new("h", 3389, "u", "", "", NULL, 256, 256));
    assert(!dh_rdp_new("h", 3389, "u", "", "", "invalid", 256, 256));
    assert(!dh_rdp_new("|external-fd", 3389, "u", "", "", pin, 256, 256));
    assert(!dh_rdp_new("/tmp/socket", 3389, "u", "", "", pin, 256, 256));
    assert(!dh_rdp_new("h", 3389, "u", "", "", pin, 199, 256));
    assert(!dh_rdp_new("h", 3389, "u", "", "", pin, 0, 1));
    assert(!dh_rdp_new("h", 3389, "u", "", "", pin, 8193, 1));
    assert(!dh_rdp_new("h", 3389, "u", "", "", pin, UINT32_MAX, UINT32_MAX));
    assert(dh_rdp_state(NULL) == DH_RDP_FAILED);
    assert(dh_rdp_error(NULL) == DH_RDP_ERROR_ARGUMENT);
    assert(dh_rdp_session_id(NULL) == UINT32_MAX);
    assert(dh_rdp_run(NULL) != 0);
    dh_rdp_stop(NULL);
    dh_rdp_free(NULL);
    void* colon = dh_rdp_new("h", 3389, "u", NULL, "",
        "01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF",
        200, 200);
    assert(colon);
    dh_rdp_free(colon);
    for (int i = 0; i < 64; i++) {
        void* s = make();
        assert(s && dh_rdp_state(s) == DH_RDP_IDLE);
        assert(dh_rdp_error(s) == 0);
        assert(dh_rdp_session_id(s) == UINT32_MAX);
        uint32_t w = 1, h = 1, stride = 1;
        uint64_t seq = 1;
        assert(dh_rdp_copy_frame(s, NULL, 0, &w, &h, &stride, &seq) == 0);
        assert(w == 0 && h == 0 && stride == 0 && seq == 0);
        if (i % 2) {
            dh_rdp_stop(s);
            assert(dh_rdp_state(s) == DH_RDP_STOPPED);
            assert(dh_rdp_run(s) == 0);
            assert(dh_rdp_run(s) != 0);
        }
        dh_rdp_free(s);
    }
    for (int i = 0; i < 32; i++) {
        void* s = make();
        pthread_t worker;
        assert(s && pthread_create(&worker, NULL, run, s) == 0);
        struct timespec delay = {0, (i % 4) * 1000000};
        nanosleep(&delay, NULL);
        dh_rdp_stop(s);
        assert(pthread_join(worker, NULL) == 0);
        assert(dh_rdp_state(s) == DH_RDP_STOPPED || dh_rdp_state(s) == DH_RDP_FAILED);
        dh_rdp_free(s);
    }
    puts("ABI: invalid config/pin, lifecycle, once-only run, stop races: PASS");
    return 0;
}
