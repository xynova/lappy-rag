package errors

// Code is a stable domain error discriminator.
type Code string

const (
	CodeInvalid     Code = "invalid"
	CodeNotFound    Code = "not_found"
	CodeConflict    Code = "conflict"
	CodeUnavailable Code = "unavailable"
	CodeDeadline    Code = "deadline"
	CodeInternal    Code = "internal"
)
