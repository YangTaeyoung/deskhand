// Copyright (c) Microsoft Corporation. Portions adapted from IddSample.
// Copyright (c) 2026 Deskhand contributors.
// Licensed under MS-PL; see LICENSE and README.md for provenance.
#define NOMINMAX
#include <windows.h>
#include <bugcodes.h>
#include <wudfwdm.h>
#include <wdf.h>
#include <iddcx.h>
#include <dxgi1_5.h>
#include <d3d11.h>
#include <avrt.h>
#include <wrl.h>
#include <new>

using Microsoft::WRL::ComPtr;

// A display source, not a video transport: the agent captures and encodes this
// desktop. Its lifetime is the software-device handle held by the controller.
struct Mode { DWORD width, height, hz; };
static constexpr Mode modes[] = {{1920, 1080, 60}, {1600, 900, 60}, {1280, 720, 60}};
static const GUID monitorContainer = {0x58cb140d, 0xa642, 0x4c16, {0xaa,0x76,0xc3,0x24,0x7e,0x35,0x40,0x71}};

static void SignalInfo(DISPLAYCONFIG_VIDEO_SIGNAL_INFO& signal, const Mode& mode, bool monitor)
{
    signal = {};
    signal.totalSize.cx = signal.activeSize.cx = mode.width;
    signal.totalSize.cy = signal.activeSize.cy = mode.height;
    signal.AdditionalSignalInfo.vSyncFreqDivider = monitor ? 0 : 1;
    signal.AdditionalSignalInfo.videoStandard = 255;
    signal.vSyncFreq = {mode.hz, 1};
    signal.hSyncFreq = {mode.hz * mode.height, 1};
    signal.pixelRate = UINT64(mode.hz) * mode.width * mode.height;
    signal.scanLineOrdering = DISPLAYCONFIG_SCANLINE_ORDERING_PROGRESSIVE;
}

// Drain each assigned swapchain promptly. No polling, frame readback or encode
// work runs in this driver. Keeping work here bounded lets DWM produce normally.
class FramePump {
    IDDCX_SWAPCHAIN chain;
    LUID adapter;
    HANDLE available;
    HANDLE stop = nullptr;
    HANDLE thread = nullptr;
    ComPtr<ID3D11Device> device;

    static DWORD WINAPI Entry(void* arg) { static_cast<FramePump*>(arg)->Run(); return 0; }
    void Run()
    {
        DWORD task = 0;
        HANDLE priority = AvSetMmThreadCharacteristicsW(L"Distribution", &task);
        Drain();
        WdfObjectDelete(chain);
        if (priority) AvRevertMmThreadCharacteristics(priority);
    }
    void Drain()
    {
        ComPtr<IDXGIFactory5> factory;
        ComPtr<IDXGIAdapter1> renderAdapter;
        ComPtr<IDXGIDevice> dxgiDevice;
        if (FAILED(CreateDXGIFactory2(0, IID_PPV_ARGS(&factory))) ||
            FAILED(factory->EnumAdapterByLuid(adapter, IID_PPV_ARGS(&renderAdapter))) ||
            FAILED(D3D11CreateDevice(renderAdapter.Get(), D3D_DRIVER_TYPE_UNKNOWN,
                nullptr, D3D11_CREATE_DEVICE_BGRA_SUPPORT, nullptr, 0,
                D3D11_SDK_VERSION, &device, nullptr, nullptr)) ||
            FAILED(device.As(&dxgiDevice))) return;

        IDARG_IN_SWAPCHAINSETDEVICE bind = {};
        bind.pDevice = dxgiDevice.Get();
        if (FAILED(IddCxSwapChainSetDevice(chain, &bind))) return;
        HANDLE waits[] = {stop, available};
        while (WaitForSingleObject(stop, 0) == WAIT_TIMEOUT) {
            IDARG_OUT_RELEASEANDACQUIREBUFFER frame = {};
            HRESULT hr = IddCxSwapChainReleaseAndAcquireBuffer(chain, &frame);
            if (hr == E_PENDING) {
                if (WaitForMultipleObjects(2, waits, FALSE, INFINITE) != WAIT_OBJECT_0 + 1) break;
                continue;
            }
            if (FAILED(hr)) break;
            // The OS retains the acquired surface until the next acquisition;
            // release our returned reference before acknowledging this frame.
            if (frame.MetaData.pSurface) frame.MetaData.pSurface->Release();
            if (FAILED(IddCxSwapChainFinishedProcessingFrame(chain))) break;
        }
    }
public:
    FramePump(IDDCX_SWAPCHAIN value, LUID luid, HANDLE event) : chain(value), adapter(luid), available(event) {}
    bool Start()
    {
        stop = CreateEventW(nullptr, TRUE, FALSE, nullptr);
        if (!stop) return false;
        thread = CreateThread(nullptr, 0, Entry, this, 0, nullptr);
        return thread != nullptr;
    }
    ~FramePump()
    {
        if (stop) SetEvent(stop);
        if (thread) { WaitForSingleObject(thread, INFINITE); CloseHandle(thread); }
        if (stop) CloseHandle(stop);
    }
};

struct DeviceContext { WDFDEVICE device; IDDCX_ADAPTER adapter; };
struct AdapterContext { WDFDEVICE device; };
struct MonitorContext { FramePump* pump; };
WDF_DECLARE_CONTEXT_TYPE_WITH_NAME(DeviceContext, DeviceState);
WDF_DECLARE_CONTEXT_TYPE_WITH_NAME(AdapterContext, AdapterState);
WDF_DECLARE_CONTEXT_TYPE_WITH_NAME(MonitorContext, MonitorState);

EVT_WDF_DRIVER_DEVICE_ADD DeviceAdd;
EVT_WDF_DEVICE_D0_ENTRY DeviceStart;
EVT_IDD_CX_ADAPTER_INIT_FINISHED AdapterReady;
EVT_IDD_CX_ADAPTER_COMMIT_MODES CommitModes;
EVT_IDD_CX_PARSE_MONITOR_DESCRIPTION ParseDescription;
EVT_IDD_CX_MONITOR_GET_DEFAULT_DESCRIPTION_MODES DefaultModes;
EVT_IDD_CX_MONITOR_QUERY_TARGET_MODES TargetModes;
EVT_IDD_CX_MONITOR_ASSIGN_SWAPCHAIN AssignChain;
EVT_IDD_CX_MONITOR_UNASSIGN_SWAPCHAIN UnassignChain;

extern "C" BOOL WINAPI DllMain(HINSTANCE, UINT, LPVOID) { return TRUE; }
extern "C" DRIVER_INITIALIZE DriverEntry;
extern "C" NTSTATUS DriverEntry(PDRIVER_OBJECT object, PUNICODE_STRING path)
{
    WDF_DRIVER_CONFIG config;
    WDF_DRIVER_CONFIG_INIT(&config, DeviceAdd);
    return WdfDriverCreate(object, path, WDF_NO_OBJECT_ATTRIBUTES, &config, WDF_NO_HANDLE);
}

_Use_decl_annotations_
NTSTATUS DeviceAdd(WDFDRIVER driver, PWDFDEVICE_INIT init)
{
    UNREFERENCED_PARAMETER(driver);
    WDF_PNPPOWER_EVENT_CALLBACKS power;
    WDF_PNPPOWER_EVENT_CALLBACKS_INIT(&power);
    power.EvtDeviceD0Entry = DeviceStart;
    WdfDeviceInitSetPnpPowerEventCallbacks(init, &power);
    IDD_CX_CLIENT_CONFIG config;
    IDD_CX_CLIENT_CONFIG_INIT(&config);
    config.EvtIddCxAdapterInitFinished = AdapterReady;
    config.EvtIddCxAdapterCommitModes = CommitModes;
    config.EvtIddCxParseMonitorDescription = ParseDescription;
    config.EvtIddCxMonitorGetDefaultDescriptionModes = DefaultModes;
    config.EvtIddCxMonitorQueryTargetModes = TargetModes;
    config.EvtIddCxMonitorAssignSwapChain = AssignChain;
    config.EvtIddCxMonitorUnassignSwapChain = UnassignChain;
    NTSTATUS status = IddCxDeviceInitConfig(init, &config);
    if (!NT_SUCCESS(status)) return status;
    WDF_OBJECT_ATTRIBUTES attrs;
    WDF_OBJECT_ATTRIBUTES_INIT_CONTEXT_TYPE(&attrs, DeviceContext);
    WDFDEVICE device;
    status = WdfDeviceCreate(&init, &attrs, &device);
    if (!NT_SUCCESS(status)) return status;
    DeviceState(device)->device = device;
    return IddCxDeviceInitialize(device);
}

_Use_decl_annotations_
NTSTATUS DeviceStart(WDFDEVICE device, WDF_POWER_DEVICE_STATE previous)
{
    UNREFERENCED_PARAMETER(previous);
    auto* context = DeviceState(device);
    IDDCX_ADAPTER_CAPS caps = {};
    caps.Size = sizeof(caps);
    caps.MaxMonitorsSupported = 1;
    auto& diagnostic = caps.EndPointDiagnostics;
    diagnostic.Size = sizeof(diagnostic);
    diagnostic.GammaSupport = IDDCX_FEATURE_IMPLEMENTATION_NONE;
    diagnostic.TransmissionType = IDDCX_TRANSMISSION_TYPE_WIRED_OTHER;
    diagnostic.pEndPointFriendlyName = L"Deskhand Virtual Display";
    diagnostic.pEndPointManufacturerName = L"Deskhand";
    diagnostic.pEndPointModelName = L"Deskhand Headless Display";
    IDDCX_ENDPOINT_VERSION version = {};
    version.Size = sizeof(version);
    version.MajorVer = 1;
    diagnostic.pFirmwareVersion = diagnostic.pHardwareVersion = &version;
    WDF_OBJECT_ATTRIBUTES attrs;
    WDF_OBJECT_ATTRIBUTES_INIT_CONTEXT_TYPE(&attrs, AdapterContext);
    IDARG_IN_ADAPTER_INIT input = {};
    input.WdfDevice = device;
    input.pCaps = &caps;
    input.ObjectAttributes = &attrs;
    IDARG_OUT_ADAPTER_INIT output = {};
    NTSTATUS status = IddCxAdapterInitAsync(&input, &output);
    if (NT_SUCCESS(status)) {
        context->adapter = output.AdapterObject;
        AdapterState(output.AdapterObject)->device = device;
    }
    return status;
}

_Use_decl_annotations_
NTSTATUS AdapterReady(IDDCX_ADAPTER adapter, const IDARG_IN_ADAPTER_INIT_FINISHED* input)
{
    if (!NT_SUCCESS(input->AdapterInitStatus)) return input->AdapterInitStatus;
    WDF_OBJECT_ATTRIBUTES attrs;
    WDF_OBJECT_ATTRIBUTES_INIT_CONTEXT_TYPE(&attrs, MonitorContext);
    attrs.EvtCleanupCallback = [](WDFOBJECT object) {
        auto* state = MonitorState(object);
        delete state->pump;
        state->pump = nullptr;
    };
    IDDCX_MONITOR_INFO info = {};
    info.Size = sizeof(info);
    info.MonitorType = DISPLAYCONFIG_OUTPUT_TECHNOLOGY_HDMI;
    info.ConnectorIndex = 0;
    info.MonitorDescription.Size = sizeof(info.MonitorDescription);
    info.MonitorDescription.Type = IDDCX_MONITOR_DESCRIPTION_TYPE_EDID;
    // No borrowed hardware EDID: DefaultModes supplies the virtual modes.
    info.MonitorContainerId = monitorContainer;
    IDARG_IN_MONITORCREATE create = {};
    create.ObjectAttributes = &attrs;
    create.pMonitorInfo = &info;
    IDARG_OUT_MONITORCREATE monitor = {};
    NTSTATUS status = IddCxMonitorCreate(adapter, &create, &monitor);
    if (!NT_SUCCESS(status)) return status;
    IDARG_OUT_MONITORARRIVAL arrival = {};
    status = IddCxMonitorArrival(monitor.MonitorObject, &arrival);
    if (!NT_SUCCESS(status)) WdfObjectDelete(monitor.MonitorObject);
    return status;
}

_Use_decl_annotations_
NTSTATUS CommitModes(IDDCX_ADAPTER adapter, const IDARG_IN_COMMITMODES* input)
{
    UNREFERENCED_PARAMETER(adapter);
    UNREFERENCED_PARAMETER(input);
    return STATUS_SUCCESS;
}

_Use_decl_annotations_
NTSTATUS ParseDescription(const IDARG_IN_PARSEMONITORDESCRIPTION* input, IDARG_OUT_PARSEMONITORDESCRIPTION* output)
{
    UNREFERENCED_PARAMETER(input);
    output->MonitorModeBufferOutputCount = 0;
    // This driver enumerates no hardware descriptor; only DefaultModes applies.
    return STATUS_INVALID_PARAMETER;
}

_Use_decl_annotations_
NTSTATUS DefaultModes(IDDCX_MONITOR monitor, const IDARG_IN_GETDEFAULTDESCRIPTIONMODES* input,
    IDARG_OUT_GETDEFAULTDESCRIPTIONMODES* output)
{
    UNREFERENCED_PARAMETER(monitor);
    output->DefaultMonitorModeBufferOutputCount = ARRAYSIZE(modes);
    output->PreferredMonitorModeIdx = 0;
    if (!input->DefaultMonitorModeBufferInputCount) return STATUS_SUCCESS;
    if (input->DefaultMonitorModeBufferInputCount < ARRAYSIZE(modes)) return STATUS_BUFFER_TOO_SMALL;
    for (UINT i = 0; i < ARRAYSIZE(modes); ++i) {
        auto& value = input->pDefaultMonitorModes[i];
        value = {};
        value.Size = sizeof(value);
        value.Origin = IDDCX_MONITOR_MODE_ORIGIN_DRIVER;
        SignalInfo(value.MonitorVideoSignalInfo, modes[i], true);
    }
    return STATUS_SUCCESS;
}

_Use_decl_annotations_
NTSTATUS TargetModes(IDDCX_MONITOR monitor, const IDARG_IN_QUERYTARGETMODES* input, IDARG_OUT_QUERYTARGETMODES* output)
{
    UNREFERENCED_PARAMETER(monitor);
    output->TargetModeBufferOutputCount = ARRAYSIZE(modes);
    if (!input->TargetModeBufferInputCount) return STATUS_SUCCESS;
    if (input->TargetModeBufferInputCount < ARRAYSIZE(modes)) return STATUS_BUFFER_TOO_SMALL;
    for (UINT i = 0; i < ARRAYSIZE(modes); ++i) {
        auto& value = input->pTargetModes[i];
        value = {};
        value.Size = sizeof(value);
        SignalInfo(value.TargetVideoSignalInfo.targetVideoSignalInfo, modes[i], false);
    }
    return STATUS_SUCCESS;
}

_Use_decl_annotations_
NTSTATUS AssignChain(IDDCX_MONITOR monitor, const IDARG_IN_SETSWAPCHAIN* input)
{
    auto* state = MonitorState(monitor);
    delete state->pump;
    state->pump = new (std::nothrow) FramePump(input->hSwapChain, input->RenderAdapterLuid, input->hNextSurfaceAvailable);
    if (!state->pump || !state->pump->Start()) {
        delete state->pump;
        state->pump = nullptr;
        // Ownership transfers only on success. IddCx releases this chain when
        // assignment is abandoned and may retry after transient resource pressure.
        return STATUS_GRAPHICS_INDIRECT_DISPLAY_ABANDON_SWAPCHAIN;
    }
    return STATUS_SUCCESS;
}

_Use_decl_annotations_
NTSTATUS UnassignChain(IDDCX_MONITOR monitor)
{
    auto* state = MonitorState(monitor);
    delete state->pump;
    state->pump = nullptr;
    return STATUS_SUCCESS;
}
