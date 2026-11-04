// Package protocol defines the stable machine-readable contract shared by all
// blog-bootstrap commands: schema version, error codes, exit codes, and the
// next_action hand-off structure.
package protocol

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the version of the JSON envelope emitted by every command.
const SchemaVersion = 1

// Exit codes are fixed by the agent protocol.
const (
	ExitOK     = 0
	ExitHuman  = 2
	ExitRetry  = 3
	ExitFatal  = 4
	ExitVerify = 5
)

// Class describes how a failure should be handled.
type Class string

const (
	ClassFatal  Class = "fatal"
	ClassHuman  Class = "human"
	ClassRetry  Class = "retry"
	ClassVerify Class = "verify"
)

// Error is a classified, machine-readable failure.
type Error struct {
	Code    string
	Message string
	Class   Class
	Next    *NextAction
	LogPath string
	Details map[string]string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// ExitCode maps a failure class onto the fixed process exit code.
func (e *Error) ExitCode() int {
	if e == nil {
		return ExitOK
	}
	switch e.Class {
	case ClassHuman:
		return ExitHuman
	case ClassRetry:
		return ExitRetry
	case ClassVerify:
		return ExitVerify
	default:
		return ExitFatal
	}
}

// Fatal builds a fatal (exit 4) error.
func Fatal(code, message string) *Error {
	return &Error{Code: code, Message: message, Class: ClassFatal}
}

// Human builds an error that needs a person to act (exit 2).
func Human(code, message string, next *NextAction) *Error {
	return &Error{Code: code, Message: message, Class: ClassHuman, Next: next}
}

// Retry builds a retryable error (exit 3).
func Retry(code, message string) *Error {
	return &Error{Code: code, Message: message, Class: ClassRetry}
}

// Verify builds a strict-verification failure (exit 5).
func Verify(code, message string) *Error {
	return &Error{Code: code, Message: message, Class: ClassVerify}
}

func (e *Error) WithCause(err error) *Error              { e.Cause = err; return e }
func (e *Error) WithLog(path string) *Error              { e.LogPath = path; return e }
func (e *Error) WithDetails(kv map[string]string) *Error { e.Details = kv; return e }

// As extracts a protocol error from err, returning nil when err is not one.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	type unwrapper interface{ Unwrap() error }
	for {
		u, ok := err.(unwrapper)
		if !ok {
			return nil
		}
		err = u.Unwrap()
		if e, ok := err.(*Error); ok {
			return e
		}
	}
}

// NextAction tells a caller (human or agent) what to do next.
type NextAction struct {
	Type     string `json:"type"` // run | human | retry | abort
	Code     string `json:"code,omitempty"`
	OpenURL  string `json:"open_url,omitempty"`
	Message  string `json:"message,omitempty"`
	Expected string `json:"expected,omitempty"`
	Poll     *Poll  `json:"poll,omitempty"`
	Command  string `json:"command,omitempty"`
}

// Poll describes how a caller should wait for a human-gated condition.
type Poll struct {
	Command         string `json:"command"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
}

// ErrorBody is the JSON form of Error without the internal class.
type ErrorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable"`
	LogPath   string            `json:"log_path,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

// Envelope is the top-level JSON document printed by every command.
type Envelope struct {
	SchemaVersion int         `json:"schema_version"`
	OK            bool        `json:"ok"`
	Command       string      `json:"command"`
	Error         *ErrorBody  `json:"error,omitempty"`
	NextAction    *NextAction `json:"next_action,omitempty"`
	Data          interface{} `json:"data,omitempty"`
}

// NewSuccess builds a successful envelope.
func NewSuccess(command string, data interface{}) Envelope {
	return Envelope{SchemaVersion: SchemaVersion, OK: true, Command: command, Data: data}
}

// NewFailure builds a failing envelope from a classified error.
func NewFailure(command string, err *Error) Envelope {
	body := &ErrorBody{
		Code:      err.Code,
		Message:   err.Message,
		Retryable: err.Class == ClassRetry,
		LogPath:   err.LogPath,
		Details:   err.Details,
	}
	next := err.Next
	if next == nil {
		switch err.Class {
		case ClassRetry:
			next = &NextAction{Type: "retry", Code: err.Code, Message: err.Message}
		case ClassHuman:
			next = &NextAction{Type: "human", Code: err.Code, Message: err.Message}
		case ClassVerify:
			next = &NextAction{Type: "abort", Code: err.Code, Message: err.Message}
		default:
			next = &NextAction{Type: "abort", Code: err.Code, Message: err.Message}
		}
	}
	return Envelope{SchemaVersion: SchemaVersion, OK: false, Command: command, Error: body, NextAction: next}
}

// Marshal encodes the envelope as indented JSON with a trailing newline.
func Marshal(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
