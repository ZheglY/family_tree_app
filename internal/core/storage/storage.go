package storage

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var ErrObjectNotFound = errors.New("object was not found")

type UploadInput struct {
	ObjectKey      string
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
}

type PresignedRequest struct {
	URL       string
	Method    string
	Headers   http.Header
	ExpiresAt time.Time
}

type ObjectInfo struct {
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	ETag           string
}

type ObjectStore interface {
	PresignUpload(context.Context, UploadInput) (PresignedRequest, error)
	HeadObject(context.Context, string) (ObjectInfo, error)
	PresignDownload(context.Context, string, string) (PresignedRequest, error)
}
