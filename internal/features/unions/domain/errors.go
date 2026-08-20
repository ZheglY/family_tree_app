package domain

import "errors"

var (
	ErrInvalidUnion         = errors.New("family union is invalid")
	ErrUnionNotFound        = errors.New("family union was not found")
	ErrUnionAccessDenied    = errors.New("family union access is denied")
	ErrUnionVersionConflict = errors.New("family union version conflict")
	ErrDuplicateUnionMember = errors.New("person is already a union member")
	ErrUnionMemberNotFound  = errors.New("union member was not found")
	ErrUnionMemberLimit     = errors.New("union member limit reached")
)
