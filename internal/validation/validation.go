// Package validation defines errors that are safe to expose to an API caller.
// Storage and infrastructure errors intentionally use their original types so
// HTTP handlers can keep them out of response bodies.
package validation

import "errors"

// ValidationError represents an input or request parameter error. Its message
// is authored by the application and is safe for a client-facing response.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

func New(message string) error { return ValidationError{Message: message} }

func Is(err error) bool {
	var target ValidationError
	return errors.As(err, &target)
}
