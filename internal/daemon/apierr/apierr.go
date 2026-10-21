// Package apierr is the coded error of the daemon: a code of
// docs/protocol.md §8, a sentence that names the next step, whether a retry
// can succeed and the next action a client can take.
package apierr

import (
	"errors"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Next actions carried in errors and result envelopes.
const (
	NextCheckState   = "check_state"
	NextHostRepair   = "host_repair"
	NextHostAddForce = "host_add_force"
	NextHostConnect  = "host_connect"
	NextRetry        = "retry"
	NextFixParams    = "fix_params"
	NextHostDoctor   = "host_doctor"
	// NextHumanRelease: a person holds control; wait until they release it.
	NextHumanRelease = "human_release"
	// NextHumanResumeAI: the emergency stop paused AI input; wait until a
	// person gives control back to the AI.
	NextHumanResumeAI = "human_resume_ai"
	// NextScreenCapture: the coordinates of a capture no longer hold; take
	// a new screen.capture and use its capture_id.
	NextScreenCapture = "screen_capture"
)

// ShellWord quotes s with single quotes when it holds a space or a tab.
func ShellWord(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "'" + s + "'"
	}
	return s
}

// HostAddForceCommand is the command of NextHostAddForce for the host name
// over the SSH alias.
func HostAddForceCommand(name, alias string) string {
	return "deskhand host add " + ShellWord(name) + " --ssh " + alias + " --force"
}

// Error is a daemon error with a protocol code.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	NextAction string `json:"next_action,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// New returns an Error.
func New(code, message string, retryable bool, next string) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, NextAction: next}
}

// Invalid returns an INVALID_ARGUMENT error.
func Invalid(message string) *Error {
	return New(wire.CodeInvalidArgument, message, false, NextFixParams)
}

// NotFound returns a NOT_FOUND error.
func NotFound(message string) *Error {
	return New(wire.CodeNotFound, message, false, NextFixParams)
}

// Internal returns an INTERNAL error.
func Internal(message string) *Error {
	return New(wire.CodeInternal, message, false, "")
}

// As returns err as an Error; any other error becomes INTERNAL with its text.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err.Error())
}

// CodeOf returns the code of err, or "" when err is not an Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
