package service

import "errors"

// CodingRuntimeError carries stable machine-readable failure metadata.
type CodingRuntimeError struct {
	Code      string
	Category  string
	Message   string
	Retryable bool
	Cause     error
}

func (e *CodingRuntimeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "coding runtime error"
}

func (e *CodingRuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newCodingRuntimeError(code, category, message string, retryable bool, cause error) error {
	return &CodingRuntimeError{
		Code:      code,
		Category:  category,
		Message:   message,
		Retryable: retryable,
		Cause:     cause,
	}
}

func codingRuntimeErrorDetails(err error) (string, string, bool, bool) {
	var runtimeErr *CodingRuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr == nil {
		return "", "", false, false
	}
	return runtimeErr.Code, runtimeErr.Category, runtimeErr.Retryable, true
}
