// Package apierr is the coded error of the daemon: a code of
// docs/protocol.md §8, a sentence that names the next step, whether a retry
// can succeed and the next action a client can take.
package apierr

import (
	"errors"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Next actions carried in errors and result envelopes.
const (
	NextCheckState  = "check_state"
	NextHostRepair  = "host_repair"
	NextHostConnect = "host_connect"
	NextRetry       = "retry"
	NextFixParams   = "fix_params"
	NextHostDoctor  = "host_doctor"
)

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
