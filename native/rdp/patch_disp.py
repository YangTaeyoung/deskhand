"""Generate a narrowly patched DISP translation unit; pinned source stays pristine."""
from pathlib import Path
import sys
source, output = map(Path, sys.argv[1:])
s = (source / 'channels/disp/client/disp_main.c').read_text()
def replace(old, new):
    global s
    if s.count(old) != 1:
        raise SystemExit('Pinned DISP patch context changed; refusing to build')
    s = s.replace(old, new)
replace('#include "disp_main.h"', '#include <pthread.h>\nstatic pthread_mutex_t dh_disp_lifetime = PTHREAD_MUTEX_INITIALIZER;\n#include "disp_main.h"')
replace('\tDispClientContext* context;\n\tUINT32 MaxNumMonitors;', '\tDispClientContext* context;\n\tGENERIC_CHANNEL_CALLBACK* active_callback;\n\tUINT32 MaxNumMonitors;')
replace('''static UINT disp_on_close(IWTSVirtualChannelCallback* pChannelCallback)
{
	free(pChannelCallback);
	return CHANNEL_RC_OK;
}''', '''/* Deskhand: pin channel callback lifetime across writes and closure. */
static UINT disp_on_open(IWTSVirtualChannelCallback* pChannelCallback)
{
    GENERIC_CHANNEL_CALLBACK* callback = (GENERIC_CHANNEL_CALLBACK*)pChannelCallback;
    DISP_PLUGIN* disp = (DISP_PLUGIN*)callback->plugin;
    pthread_mutex_lock(&dh_disp_lifetime);
    disp->active_callback = callback;
    pthread_mutex_unlock(&dh_disp_lifetime);
    return CHANNEL_RC_OK;
}
static UINT disp_on_close(IWTSVirtualChannelCallback* pChannelCallback)
{
    GENERIC_CHANNEL_CALLBACK* callback = (GENERIC_CHANNEL_CALLBACK*)pChannelCallback;
    DISP_PLUGIN* disp = (DISP_PLUGIN*)callback->plugin;
    pthread_mutex_lock(&dh_disp_lifetime);
    if (disp->active_callback == callback) disp->active_callback = NULL;
    free(pChannelCallback);
    pthread_mutex_unlock(&dh_disp_lifetime);
    return CHANNEL_RC_OK;
}''')
replace('''	callback = disp->base.listener_callback->channel_callback;

	return disp_send_display_control_monitor_layout_pdu(callback, NumMonitors, Monitors);''', '''    pthread_mutex_lock(&dh_disp_lifetime);
    callback = disp->active_callback;
    UINT result = callback ? disp_send_display_control_monitor_layout_pdu(callback, NumMonitors, Monitors) : ERROR_NOT_CONNECTED;
    pthread_mutex_unlock(&dh_disp_lifetime);
    return result;''')
replace('nullptr, /* Open */', 'disp_on_open, /* Deskhand lifetime tracking */')
output.mkdir(parents=True, exist_ok=True)
(output / 'client').mkdir(exist_ok=True)
(output / 'client/disp_main.c').write_text(s)
for name in ['client/disp_main.h', 'disp_common.h']:
    (output / name).write_bytes((source / 'channels/disp' / name).read_bytes())
