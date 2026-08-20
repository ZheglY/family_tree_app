package domain

import "errors"

var (
	ErrInvalidRelation         = errors.New("parent-child relation is invalid")
	ErrRelationNotFound        = errors.New("parent-child relation was not found")
	ErrRelationAccessDenied    = errors.New("parent-child relation access is denied")
	ErrRelationVersionConflict = errors.New("parent-child relation version conflict")
	ErrDuplicateRelation       = errors.New("parent-child relation already exists")
	ErrRelationCycle           = errors.New("parent-child relation creates a cycle")
	ErrGraphLimitExceeded      = errors.New("graph node limit exceeded")
)
