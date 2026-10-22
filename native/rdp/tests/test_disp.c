/* Exercise the actual generated FreeRDP DISP lifecycle, including a blocked
 * channel write overlapping OnClose. No remote server or password needed. */
#define disp_DVCPluginEntry dh_test_disp_DVCPluginEntry
#include "disp_main.c"
#undef TAG
#include "disp_common.c"
#include <assert.h>
#include <stdatomic.h>
#include <unistd.h>
static pthread_mutex_t test_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t test_ready = PTHREAD_COND_INITIALIZER;
static bool writing, release_write;
static atomic_bool close_started, close_done;
static UINT blocked_write(IWTSVirtualChannel* channel, UINT32 count, const BYTE* bytes, void* reserved) {
    (void)channel; (void)count; (void)bytes; (void)reserved;
    pthread_mutex_lock(&test_lock);
    writing = true;
    pthread_cond_broadcast(&test_ready);
    while (!release_write) pthread_cond_wait(&test_ready, &test_lock);
    pthread_mutex_unlock(&test_lock);
    return CHANNEL_RC_OK;
}
static void* send_layout(void* context) {
    DISPLAY_CONTROL_MONITOR_LAYOUT layout = {.Flags = 1, .Width = 800, .Height = 600};
    assert(disp_send_monitor_layout(context, 1, &layout) == CHANNEL_RC_OK);
    return NULL;
}
static void* close_channel(void* callback) {
    atomic_store(&close_started, true);
    assert(disp_on_close(callback) == CHANNEL_RC_OK);
    atomic_store(&close_done, true);
    return NULL;
}
int main(void) {
    DISP_PLUGIN plugin = {.MaxNumMonitors = 1};
    plugin.base.log = WLog_Get("deskhand.disp.test");
    DispClientContext context = {.handle = &plugin};
    IWTSVirtualChannel channel = {.Write = blocked_write};
    GENERIC_CHANNEL_CALLBACK* callback = calloc(1, sizeof(*callback));
    assert(callback);
    callback->plugin = (IWTSPlugin*)&plugin; callback->channel = &channel;
    assert(disp_on_open((IWTSVirtualChannelCallback*)callback) == CHANNEL_RC_OK);
    pthread_t sender, closer;
    assert(pthread_create(&sender, NULL, send_layout, &context) == 0);
    pthread_mutex_lock(&test_lock);
    while (!writing) pthread_cond_wait(&test_ready, &test_lock);
    pthread_mutex_unlock(&test_lock);
    assert(pthread_create(&closer, NULL, close_channel, callback) == 0);
    while (!atomic_load(&close_started)) usleep(100);
    usleep(20000);
    assert(!atomic_load(&close_done)); /* Free must wait until the write returns. */
    pthread_mutex_lock(&test_lock); release_write = true;
    pthread_cond_broadcast(&test_ready); pthread_mutex_unlock(&test_lock);
    assert(pthread_join(sender, NULL) == 0 && pthread_join(closer, NULL) == 0);
    assert(plugin.active_callback == NULL);
    DISPLAY_CONTROL_MONITOR_LAYOUT layout = {.Width = 800, .Height = 600};
    assert(disp_send_monitor_layout(&context, 1, &layout) == ERROR_NOT_CONNECTED);
    puts("Actual DISP send/close serialization and post-close rejection: PASS");
    return 0;
}
