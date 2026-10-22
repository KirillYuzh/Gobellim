package entity

import "errors"

// Ошибки-сентинелы. HTTP-код для каждой выбирает internal/errmap.
var (
	ErrInvalid      = errors.New("invalid request")
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
	ErrUpstream     = errors.New("upstream unavailable")
	ErrBudget       = errors.New("budget exceeded")
	ErrInternal     = errors.New("internal error")
	ErrConfig       = errors.New("invalid configuration")
)
