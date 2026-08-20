package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/request"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/ZheglY/family_tree_app/internal/features/media/service"
	"github.com/google/uuid"
)

type MediaService interface {
	CreateUploadIntent(context.Context, service.UploadIntentCommand) (service.UploadIntentResult, error)
	CompleteUpload(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (service.Result, error)
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (service.Result, error)
	List(context.Context, service.ListCommand) (service.ListResult, error)
	Update(context.Context, service.UpdateCommand) (service.Result, error)
	Delete(context.Context, service.MutationCommand) error
	AttachToPerson(context.Context, service.AttachCommand) (service.AttachmentResult, error)
	DetachFromPerson(context.Context, service.DetachCommand) error
	SetPrimaryPersonMedia(context.Context, service.SetPrimaryCommand) (service.PrimaryMediaResult, error)
}

type Handler struct {
	service       MediaService
	requireAccess coremiddleware.Middleware
}

func NewHandler(service MediaService, requireAccess coremiddleware.Middleware) *Handler {
	return &Handler{service: service, requireAccess: requireAccess}
}

func (h *Handler) Routes() []server.Route {
	protected := []coremiddleware.Middleware{h.requireAccess}
	return []server.Route{
		{Method: http.MethodPost, Path: "/trees/{tree_id}/media/upload-intents", Handler: h.CreateUploadIntent, Middleware: protected},
		{Method: http.MethodPost, Path: "/trees/{tree_id}/media/{media_id}/complete", Handler: h.CompleteUpload, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/media", Handler: h.List, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/media/{media_id}", Handler: h.Get, Middleware: protected},
		{Method: http.MethodPatch, Path: "/trees/{tree_id}/media/{media_id}", Handler: h.Update, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}/media/{media_id}", Handler: h.Delete, Middleware: protected},
		{Method: http.MethodPost, Path: "/trees/{tree_id}/persons/{person_id}/media", Handler: h.AttachToPerson, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}/persons/{person_id}/media/{media_id}", Handler: h.DetachFromPerson, Middleware: protected},
		{Method: http.MethodPut, Path: "/trees/{tree_id}/persons/{person_id}/primary-media", Handler: h.SetPrimaryPersonMedia, Middleware: protected},
	}
}

type uploadIntentRequest struct {
	ClientRequestID  uuid.UUID `json:"client_request_id"`
	Kind             string    `json:"kind"`
	OriginalFilename string    `json:"original_filename"`
	MIMEType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	ChecksumSHA256   string    `json:"checksum_sha256"`
}

type updateRequest struct {
	Version     int     `json:"version"`
	Caption     *string `json:"caption"`
	Description *string `json:"description"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type attachRequest struct {
	MediaID   uuid.UUID `json:"media_id"`
	Role      string    `json:"role"`
	SortOrder int       `json:"sort_order"`
}

type primaryMediaRequest struct {
	MediaID       uuid.UUID `json:"media_id"`
	PersonVersion int       `json:"person_version"`
}

type mediaDTO struct {
	ID               uuid.UUID  `json:"id"`
	TreeID           uuid.UUID  `json:"tree_id"`
	ClientRequestID  uuid.UUID  `json:"client_request_id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	OriginalFilename string     `json:"original_filename"`
	MIMEType         string     `json:"mime_type"`
	SizeBytes        int64      `json:"size_bytes"`
	ChecksumSHA256   string     `json:"checksum_sha256"`
	ETag             string     `json:"etag"`
	Width            *int       `json:"width,omitempty"`
	Height           *int       `json:"height,omitempty"`
	Caption          string     `json:"caption"`
	Description      string     `json:"description"`
	UploadedBy       uuid.UUID  `json:"uploaded_by"`
	UploadedAt       *time.Time `json:"uploaded_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Version          int        `json:"version"`
}

type presignedRequestDTO struct {
	URL       string              `json:"url"`
	Method    string              `json:"method"`
	Headers   map[string][]string `json:"headers"`
	ExpiresAt time.Time           `json:"expires_at"`
}

type accessDTO struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type mediaResponse struct {
	Media    mediaDTO             `json:"media"`
	Download *presignedRequestDTO `json:"download,omitempty"`
	Access   accessDTO            `json:"access"`
}

type uploadIntentResponse struct {
	Media   mediaDTO             `json:"media"`
	Upload  *presignedRequestDTO `json:"upload,omitempty"`
	Created bool                 `json:"created"`
	Access  accessDTO            `json:"access"`
}

type mediaListItem struct {
	Media    mediaDTO             `json:"media"`
	Download *presignedRequestDTO `json:"download,omitempty"`
}

type mediaListResponse struct {
	Items      []mediaListItem `json:"items"`
	NextCursor string          `json:"next_cursor"`
	Access     accessDTO       `json:"access"`
}

type attachmentDTO struct {
	TreeID    uuid.UUID `json:"tree_id"`
	PersonID  uuid.UUID `json:"person_id"`
	MediaID   uuid.UUID `json:"media_id"`
	Role      string    `json:"role"`
	SortOrder int       `json:"sort_order"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type attachmentResponse struct {
	Attachment attachmentDTO `json:"attachment"`
	Media      mediaDTO      `json:"media"`
	Access     accessDTO     `json:"access"`
}

type primaryMediaResponse struct {
	PersonID      uuid.UUID `json:"person_id"`
	MediaID       uuid.UUID `json:"media_id"`
	PersonVersion int       `json:"person_version"`
	Access        accessDTO `json:"access"`
}

func (h *Handler) CreateUploadIntent(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	var body uploadIntentRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	result, err := h.service.CreateUploadIntent(httpRequest.Context(), service.UploadIntentCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		Values: domain.CreateValues{
			ClientRequestID:  body.ClientRequestID,
			Kind:             body.Kind,
			OriginalFilename: body.OriginalFilename,
			MIMEType:         body.MIMEType,
			SizeBytes:        body.SizeBytes,
			ChecksumSHA256:   body.ChecksumSHA256,
		},
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media upload intent could not be created")
		return
	}
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(rw, httpRequest, mapUploadIntentResult(result), status)
}

func (h *Handler) CompleteUpload(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, mediaID, ok := principalTreeAndMedia(rw, httpRequest)
	if !ok {
		return
	}
	result, err := h.service.CompleteUpload(httpRequest.Context(), principal.UserID, treeID, mediaID)
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media upload could not be completed")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) Get(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, mediaID, ok := principalTreeAndMedia(rw, httpRequest)
	if !ok {
		return
	}
	result, err := h.service.Get(httpRequest.Context(), principal.UserID, treeID, mediaID)
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) List(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	limit := 0
	if raw := httpRequest.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeMediaError(rw, httpRequest, domain.ErrInvalidMedia, "Media filters are invalid")
			return
		}
		limit = parsed
	}
	result, err := h.service.List(httpRequest.Context(), service.ListCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		Kind:        httpRequest.URL.Query().Get("kind"),
		Status:      httpRequest.URL.Query().Get("status"),
		Cursor:      httpRequest.URL.Query().Get("cursor"),
		Limit:       limit,
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media could not be listed")
		return
	}
	writeJSON(rw, httpRequest, mapListResult(result), http.StatusOK)
}

func (h *Handler) Update(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, mediaID, ok := principalTreeAndMedia(rw, httpRequest)
	if !ok {
		return
	}
	var body updateRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	result, err := h.service.Update(httpRequest.Context(), service.UpdateCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		MediaID:     mediaID,
		Version:     body.Version,
		Values: domain.UpdateValues{
			Caption:     body.Caption,
			Description: body.Description,
		},
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media could not be updated")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) Delete(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, mediaID, ok := principalTreeAndMedia(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	err := h.service.Delete(httpRequest.Context(), service.MutationCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		MediaID:     mediaID,
		Version:     body.Version,
		RequestID:   requestid.FromContext(httpRequest.Context()),
		IPAddress:   directIPAddress(httpRequest),
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media could not be deleted")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AttachToPerson(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	var body attachRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	result, err := h.service.AttachToPerson(httpRequest.Context(), service.AttachCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		PersonID:    personID,
		MediaID:     body.MediaID,
		Role:        body.Role,
		SortOrder:   body.SortOrder,
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media could not be attached to the person")
		return
	}
	writeJSON(rw, httpRequest, mapAttachmentResult(result), http.StatusCreated)
}

func (h *Handler) DetachFromPerson(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	mediaID, err := uuid.Parse(httpRequest.PathValue("media_id"))
	if err != nil || mediaID == uuid.Nil {
		writeMediaError(rw, httpRequest, domain.ErrMediaAttachmentNotFound, "Media attachment was not found")
		return
	}
	err = h.service.DetachFromPerson(httpRequest.Context(), service.DetachCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		PersonID:    personID,
		MediaID:     mediaID,
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Media could not be detached from the person")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SetPrimaryPersonMedia(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	var body primaryMediaRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	result, err := h.service.SetPrimaryPersonMedia(httpRequest.Context(), service.SetPrimaryCommand{
		ActorUserID:   principal.UserID,
		TreeID:        treeID,
		PersonID:      personID,
		MediaID:       body.MediaID,
		PersonVersion: body.PersonVersion,
	})
	if err != nil {
		writeMediaError(rw, httpRequest, err, "Primary media could not be selected")
		return
	}
	writeJSON(rw, httpRequest, primaryMediaResponse{
		PersonID:      result.PersonID,
		MediaID:       result.MediaID,
		PersonVersion: result.PersonVersion,
		Access:        mapAccess(result.Membership.Role, result.Membership.Status),
	}, http.StatusOK)
}

func principalAndTree(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, bool) {
	principal, ok := access.PrincipalFromContext(httpRequest.Context())
	if !ok {
		response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
			ErrorResponse(apperrors.ErrUnauthorized, "Authentication is required")
		return access.Principal{}, uuid.Nil, false
	}
	treeID, err := uuid.Parse(httpRequest.PathValue("tree_id"))
	if err != nil || treeID == uuid.Nil {
		writeMediaError(rw, httpRequest, domain.ErrMediaNotFound, "Media was not found")
		return access.Principal{}, uuid.Nil, false
	}
	return principal, treeID, true
}

func principalTreeAndMedia(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, uuid.UUID, bool) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	mediaID, err := uuid.Parse(httpRequest.PathValue("media_id"))
	if err != nil || mediaID == uuid.Nil {
		writeMediaError(rw, httpRequest, domain.ErrMediaNotFound, "Media was not found")
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	return principal, treeID, mediaID, true
}

func principalTreeAndPerson(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, uuid.UUID, bool) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	personID, err := uuid.Parse(httpRequest.PathValue("person_id"))
	if err != nil || personID == uuid.Nil {
		writeMediaError(rw, httpRequest, domain.ErrMediaNotFound, "Person was not found")
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	return principal, treeID, personID, true
}

func decodeBody(rw http.ResponseWriter, httpRequest *http.Request, destination any) bool {
	if err := request.DecodeJSON(httpRequest.Body, destination); err != nil {
		writeMediaError(
			rw,
			httpRequest,
			fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err),
			"Request body is invalid",
		)
		return false
	}
	return true
}

func directIPAddress(httpRequest *http.Request) string {
	host, _, err := net.SplitHostPort(httpRequest.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(httpRequest.RemoteAddr) != nil {
		return httpRequest.RemoteAddr
	}
	return ""
}

func writeMediaError(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, domain.ErrInvalidMedia):
		err = fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err)
	case errors.Is(err, domain.ErrMediaNotFound),
		errors.Is(err, domain.ErrUploadedObjectNotFound),
		errors.Is(err, domain.ErrMediaAttachmentNotFound):
		err = fmt.Errorf("%w: %v", apperrors.ErrNotFound, err)
	case errors.Is(err, domain.ErrMediaAccessDenied):
		err = fmt.Errorf("%w: %v", apperrors.ErrForbidden, err)
	case errors.Is(err, domain.ErrMediaVersionConflict),
		errors.Is(err, domain.ErrPrimaryMediaConflict),
		errors.Is(err, domain.ErrMediaRequestConflict),
		errors.Is(err, domain.ErrMediaStateConflict),
		errors.Is(err, domain.ErrDuplicateMediaAttachment):
		err = fmt.Errorf("%w: %v", apperrors.ErrConflict, err)
	case errors.Is(err, domain.ErrUploadedObjectMismatch):
		err = fmt.Errorf("%w: %v", apperrors.ErrUnprocessable, err)
	}
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		ErrorResponse(err, message)
}

func writeJSON(rw http.ResponseWriter, httpRequest *http.Request, body any, status int) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		JSONResponse(body, status)
}

func mapUploadIntentResult(result service.UploadIntentResult) uploadIntentResponse {
	return uploadIntentResponse{
		Media:   mapMedia(result.Asset),
		Upload:  mapPresigned(result.Upload),
		Created: result.Created,
		Access:  mapAccess(result.Membership.Role, result.Membership.Status),
	}
}

func mapResult(result service.Result) mediaResponse {
	return mediaResponse{
		Media:    mapMedia(result.Asset),
		Download: mapPresigned(result.Download),
		Access:   mapAccess(result.Membership.Role, result.Membership.Status),
	}
}

func mapListResult(result service.ListResult) mediaListResponse {
	items := make([]mediaListItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mediaListItem{
			Media:    mapMedia(item.Asset),
			Download: mapPresigned(item.Download),
		})
	}
	return mediaListResponse{
		Items:      items,
		NextCursor: result.NextCursor,
		Access:     mapAccess(result.Membership.Role, result.Membership.Status),
	}
}

func mapAttachmentResult(result service.AttachmentResult) attachmentResponse {
	attachment := result.Attachment
	return attachmentResponse{
		Attachment: attachmentDTO{
			TreeID:    attachment.TreeID,
			PersonID:  attachment.PersonID,
			MediaID:   attachment.MediaID,
			Role:      attachment.Role,
			SortOrder: attachment.SortOrder,
			CreatedBy: attachment.CreatedBy,
			CreatedAt: attachment.CreatedAt,
		},
		Media:  mapMedia(result.Asset),
		Access: mapAccess(result.Membership.Role, result.Membership.Status),
	}
}

func mapMedia(asset domain.MediaAsset) mediaDTO {
	return mediaDTO{
		ID:               asset.ID,
		TreeID:           asset.TreeID,
		ClientRequestID:  asset.ClientRequestID,
		Kind:             asset.Kind,
		Status:           asset.Status,
		OriginalFilename: asset.OriginalFilename,
		MIMEType:         asset.MIMEType,
		SizeBytes:        asset.SizeBytes,
		ChecksumSHA256:   asset.ChecksumSHA256,
		ETag:             asset.ETag,
		Width:            asset.Width,
		Height:           asset.Height,
		Caption:          asset.Caption,
		Description:      asset.Description,
		UploadedBy:       asset.UploadedBy,
		UploadedAt:       asset.UploadedAt,
		CreatedAt:        asset.CreatedAt,
		UpdatedAt:        asset.UpdatedAt,
		Version:          asset.Version,
	}
}

func mapPresigned(request *storage.PresignedRequest) *presignedRequestDTO {
	if request == nil {
		return nil
	}
	return &presignedRequestDTO{
		URL:       request.URL,
		Method:    request.Method,
		Headers:   map[string][]string(request.Headers),
		ExpiresAt: request.ExpiresAt,
	}
}

func mapAccess(role string, status string) accessDTO {
	return accessDTO{Role: role, Status: status}
}
