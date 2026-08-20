package domain

import "errors"

var (
	ErrInvalidPerson         = errors.New("person is invalid")
	ErrPersonNotFound        = errors.New("person was not found")
	ErrPersonAccessDenied    = errors.New("person access is denied")
	ErrPersonVersionConflict = errors.New("person version conflict")
	ErrPersonNotDeleted      = errors.New("person is not deleted")
	ErrInvalidListCursor     = errors.New("person list cursor is invalid")
)
