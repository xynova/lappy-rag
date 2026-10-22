package errors

import (
	stderrors "errors"
	"fmt"
)

// DomainError is a typed error with stable code and operation name.
type DomainError struct {
	Code Code
	Op   string
	Msg  string
	Err  error
}

func (e *DomainError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Msg, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Msg)
}

func (e *DomainError) Unwrap() error {
	return e.Err
}

// Wrap attaches a domain code and operation to a cause.
func Wrap(err error, code Code, op, msg string) error {
	if err == nil {
		return nil
	}
	return &DomainError{Code: code, Op: op, Msg: msg, Err: err}
}

// New returns a domain error without a wrapped cause.
func New(code Code, op, msg string) error {
	return &DomainError{Code: code, Op: op, Msg: msg}
}

// CodeOf returns the domain code when err is or wraps DomainError.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var de *DomainError
	if As(err, &de) {
		return de.Code
	}
	return ""
}

// As is a thin wrapper around errors.As for DomainError.
func As(err error, target **DomainError) bool {
	var de *DomainError
	if stderrors.As(err, &de) {
		*target = de
		return true
	}
	return false
}
