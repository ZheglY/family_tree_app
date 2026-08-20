package domain

import "errors"

var (
	ErrInvalidTree         = errors.New("family tree is invalid")
	ErrTreeNotFound        = errors.New("family tree was not found")
	ErrTreeAccessDenied    = errors.New("family tree access is denied")
	ErrTreeVersionConflict = errors.New("family tree version conflict")
	ErrTreeNotDeleted      = errors.New("family tree is not deleted")
)
