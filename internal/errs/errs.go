// Package errs provides typed errors and exit-code mapping for the flow CLI.
//
// Conventions (see plan):
//
//	0   success
//	1   user error (bad args, missing entity, validation, missing prerequisite)
//	2   system error (exec/IO failure)
//	130 interrupted by signal
//
// All user-facing errors should carry an actionable hint so the message follows
// the format: "Error: <what> | Hint: <example>".
package errs

import (
	"errors"
	"fmt"
)

// Kind classifies an error for exit-code mapping.
type Kind int

const (
	KindUser Kind = iota + 1
	KindSystem
	KindInterrupted
)

// Error is the standard structured error returned across the codebase.
type Error struct {
	Kind Kind
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil && e.Msg == "" {
		return e.Err.Error()
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// User builds a user-facing error with an actionable hint.
func User(msg, hint string) error {
	return &Error{Kind: KindUser, Msg: msg, Hint: hint}
}

// UserWrap wraps an underlying error as a user error.
func UserWrap(err error, msg, hint string) error {
	return &Error{Kind: KindUser, Msg: msg, Hint: hint, Err: err}
}

// System reports an unexpected system/runtime failure.
func System(msg string, err error) error {
	return &Error{Kind: KindSystem, Msg: msg, Err: err}
}

// Interrupted reports a signal interruption.
func Interrupted(msg string) error {
	return &Error{Kind: KindInterrupted, Msg: msg}
}

// Format renders an error for terminal display.
func Format(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		if e.Hint != "" {
			return fmt.Sprintf("Error: %s\nHint:  %s", e.Error(), e.Hint)
		}
		return "Error: " + e.Error()
	}
	return "Error: " + err.Error()
}

// ExitCode maps an error to the process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Kind {
		case KindUser:
			return 1
		case KindSystem:
			return 2
		case KindInterrupted:
			return 130
		}
	}
	return 1
}
