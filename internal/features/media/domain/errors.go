package domain

import "errors"

var (
	ErrInvalidMedia             = errors.New("media asset is invalid")
	ErrMediaNotFound            = errors.New("media asset was not found")
	ErrMediaAccessDenied        = errors.New("media asset access is denied")
	ErrMediaVersionConflict     = errors.New("media asset version conflict")
	ErrMediaRequestConflict     = errors.New("media upload request conflicts with an existing request")
	ErrMediaStateConflict       = errors.New("media asset state does not permit this operation")
	ErrUploadedObjectNotFound   = errors.New("uploaded object was not found")
	ErrUploadedObjectMismatch   = errors.New("uploaded object metadata does not match the upload intent")
	ErrDuplicateMediaAttachment = errors.New("media is already attached to the person")
	ErrMediaAttachmentNotFound  = errors.New("media attachment was not found")
	ErrPrimaryMediaConflict     = errors.New("person version conflict while selecting primary media")
)
