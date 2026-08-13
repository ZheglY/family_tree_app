package apperrors

import "errors"

var (
	ErrNotFound = errors.New("not found")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrConflict = errors.New("conflict")
)