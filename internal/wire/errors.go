package wire

// Error codes carried in rpc results, stream rejections and input acks.
const (
	CodeOffline                 = "OFFLINE"
	CodeAuthRequired            = "AUTH_REQUIRED"
	CodeSessionLocked           = "SESSION_LOCKED"
	CodeSessionChanged          = "SESSION_CHANGED"
	CodeControlHeldByHuman      = "CONTROL_HELD_BY_HUMAN"
	CodeControlPreempted        = "CONTROL_PREEMPTED"
	CodeStaleTarget             = "STALE_TARGET"
	CodeDisplayUnavailable      = "DISPLAY_UNAVAILABLE"
	CodeElevationRequired       = "ELEVATION_REQUIRED"
	CodeCapabilityUnsupported   = "CAPABILITY_UNSUPPORTED"
	CodePortConflict            = "PORT_CONFLICT"
	CodeTransferConflict        = "TRANSFER_CONFLICT"
	CodeHashMismatch            = "HASH_MISMATCH"
	CodeDiskFull                = "DISK_FULL"
	CodeNotFound                = "NOT_FOUND"
	CodePermissionDenied        = "PERMISSION_DENIED"
	CodeInvalidArgument         = "INVALID_ARGUMENT"
	CodeInProgress              = "IN_PROGRESS"
	CodeExecutionOutcomeUnknown = "EXECUTION_OUTCOME_UNKNOWN"
	CodeTimeout                 = "TIMEOUT"
	CodeExitedEarly             = "EXITED_EARLY"
	CodeInternal                = "INTERNAL"
)
