package transport

import (
	"context"
	"encoding/json"
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
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/service"
	"github.com/google/uuid"
)

type ExportService interface {
	Create(context.Context, service.CreateCommand) (service.CreateResult, error)
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (service.Result, error)
	List(context.Context, service.ListCommand) (service.ListResult, error)
	Download(context.Context, service.MutationCommand) (service.Result, error)
	Delete(context.Context, service.MutationCommand) error
}

type Handler struct {
	service       ExportService
	requireAccess coremiddleware.Middleware
}

func NewHandler(service ExportService, requireAccess coremiddleware.Middleware) *Handler {
	return &Handler{service: service, requireAccess: requireAccess}
}

func (handler *Handler) Routes() []server.Route {
	protected := []coremiddleware.Middleware{handler.requireAccess}
	return []server.Route{
		{Method: http.MethodPost, Path: "/trees/{tree_id}/exports", Handler: handler.Create, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/exports", Handler: handler.List, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/exports/{export_id}", Handler: handler.Get, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/exports/{export_id}/download", Handler: handler.Download, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}/exports/{export_id}", Handler: handler.Delete, Middleware: protected},
	}
}

type createRequest struct {
	ClientRequestID uuid.UUID `json:"client_request_id"`
	Format          string    `json:"format"`
}

type exportDTO struct {
	ID                   uuid.UUID       `json:"id"`
	TreeID               uuid.UUID       `json:"tree_id"`
	ClientRequestID      uuid.UUID       `json:"client_request_id"`
	RequestedBy          uuid.UUID       `json:"requested_by"`
	Format               string          `json:"format"`
	SchemaVersion        int             `json:"schema_version"`
	Parameters           json.RawMessage `json:"parameters"`
	Status               string          `json:"status"`
	Progress             int             `json:"progress"`
	ResultMIMEType       string          `json:"result_mime_type,omitempty"`
	ResultSizeBytes      int64           `json:"result_size_bytes,omitempty"`
	ResultChecksumSHA256 string          `json:"result_checksum_sha256,omitempty"`
	ErrorCode            string          `json:"error_code,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	StartedAt            *time.Time      `json:"started_at,omitempty"`
	FinishedAt           *time.Time      `json:"finished_at,omitempty"`
	ExpiresAt            *time.Time      `json:"expires_at,omitempty"`
}

type accessDTO struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type presignedRequestDTO struct {
	URL       string              `json:"url"`
	Method    string              `json:"method"`
	Headers   map[string][]string `json:"headers"`
	ExpiresAt time.Time           `json:"expires_at"`
}

type exportResponse struct {
	Export   exportDTO            `json:"export"`
	Download *presignedRequestDTO `json:"download,omitempty"`
	Created  *bool                `json:"created,omitempty"`
	Access   accessDTO            `json:"access"`
}

type listResponse struct {
	Items      []exportDTO `json:"items"`
	NextCursor string      `json:"next_cursor"`
	Access     accessDTO   `json:"access"`
}

func (handler *Handler) Create(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	var body createRequest
	if err := request.DecodeJSON(httpRequest.Body, &body); err != nil {
		writeExportError(rw, httpRequest, fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err), "Export request is invalid")
		return
	}
	result, err := handler.service.Create(httpRequest.Context(), service.CreateCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		Values: domain.CreateValues{
			ClientRequestID: body.ClientRequestID,
			Format:          body.Format,
		},
		RequestID: requestid.FromContext(httpRequest.Context()),
		IPAddress: directIPAddress(httpRequest),
	})
	if err != nil {
		writeExportError(rw, httpRequest, err, "Export could not be created")
		return
	}
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	created := result.Created
	writeJSON(rw, httpRequest, mapResult(result.Result, &created), status)
}

func (handler *Handler) Get(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, exportID, ok := principalTreeAndExport(rw, httpRequest)
	if !ok {
		return
	}
	result, err := handler.service.Get(httpRequest.Context(), principal.UserID, treeID, exportID)
	if err != nil {
		writeExportError(rw, httpRequest, err, "Export could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result, nil), http.StatusOK)
}

func (handler *Handler) List(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	limit := 0
	if raw := httpRequest.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeExportError(rw, httpRequest, domain.ErrInvalidExport, "Export filters are invalid")
			return
		}
		limit = parsed
	}
	result, err := handler.service.List(httpRequest.Context(), service.ListCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		Cursor:      httpRequest.URL.Query().Get("cursor"),
		Limit:       limit,
	})
	if err != nil {
		writeExportError(rw, httpRequest, err, "Exports could not be listed")
		return
	}
	items := make([]exportDTO, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapExport(item))
	}
	writeJSON(rw, httpRequest, listResponse{
		Items: items, NextCursor: result.NextCursor,
		Access: accessDTO{Role: result.Membership.Role, Status: result.Membership.Status},
	}, http.StatusOK)
}

func (handler *Handler) Download(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, exportID, ok := principalTreeAndExport(rw, httpRequest)
	if !ok {
		return
	}
	result, err := handler.service.Download(httpRequest.Context(), service.MutationCommand{
		ActorUserID: principal.UserID, TreeID: treeID, ExportID: exportID,
		RequestID: requestid.FromContext(httpRequest.Context()), IPAddress: directIPAddress(httpRequest),
	})
	if err != nil {
		writeExportError(rw, httpRequest, err, "Export download could not be prepared")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result, nil), http.StatusOK)
}

func (handler *Handler) Delete(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, exportID, ok := principalTreeAndExport(rw, httpRequest)
	if !ok {
		return
	}
	err := handler.service.Delete(httpRequest.Context(), service.MutationCommand{
		ActorUserID: principal.UserID, TreeID: treeID, ExportID: exportID,
		RequestID: requestid.FromContext(httpRequest.Context()), IPAddress: directIPAddress(httpRequest),
	})
	if err != nil {
		writeExportError(rw, httpRequest, err, "Export could not be deleted")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func principalAndTree(rw http.ResponseWriter, httpRequest *http.Request) (access.Principal, uuid.UUID, bool) {
	principal, ok := access.PrincipalFromContext(httpRequest.Context())
	if !ok {
		response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
			ErrorResponse(apperrors.ErrUnauthorized, "Authentication is required")
		return access.Principal{}, uuid.Nil, false
	}
	treeID, err := uuid.Parse(httpRequest.PathValue("tree_id"))
	if err != nil || treeID == uuid.Nil {
		writeExportError(rw, httpRequest, domain.ErrExportNotFound, "Export was not found")
		return access.Principal{}, uuid.Nil, false
	}
	return principal, treeID, true
}

func principalTreeAndExport(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, uuid.UUID, bool) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	exportID, err := uuid.Parse(httpRequest.PathValue("export_id"))
	if err != nil || exportID == uuid.Nil {
		writeExportError(rw, httpRequest, domain.ErrExportNotFound, "Export was not found")
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	return principal, treeID, exportID, true
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

func writeExportError(rw http.ResponseWriter, httpRequest *http.Request, err error, message string) {
	switch {
	case errors.Is(err, domain.ErrInvalidExport):
		err = fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err)
	case errors.Is(err, domain.ErrExportNotFound):
		err = fmt.Errorf("%w: %v", apperrors.ErrNotFound, err)
	case errors.Is(err, domain.ErrExportAccessDenied):
		err = fmt.Errorf("%w: %v", apperrors.ErrForbidden, err)
	case errors.Is(err, domain.ErrExportRequestConflict),
		errors.Is(err, domain.ErrExportStateConflict),
		errors.Is(err, domain.ErrExportResultUnavailable):
		err = fmt.Errorf("%w: %v", apperrors.ErrConflict, err)
	}
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).ErrorResponse(err, message)
}

func writeJSON(rw http.ResponseWriter, httpRequest *http.Request, body any, status int) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).JSONResponse(body, status)
}

func mapResult(result service.Result, created *bool) exportResponse {
	response := exportResponse{
		Export: mapExport(result.Export), Created: created,
		Access: accessDTO{Role: result.Membership.Role, Status: result.Membership.Status},
	}
	if result.Download != nil {
		response.Download = mapPresigned(*result.Download)
	}
	return response
}

func mapExport(value domain.Export) exportDTO {
	return exportDTO{
		ID: value.ID, TreeID: value.TreeID, ClientRequestID: value.ClientRequestID,
		RequestedBy: value.RequestedBy, Format: value.Format, SchemaVersion: value.SchemaVersion,
		Parameters: value.Parameters, Status: value.Status, Progress: value.Progress,
		ResultMIMEType: value.ResultMIMEType, ResultSizeBytes: value.ResultSizeBytes,
		ResultChecksumSHA256: value.ResultChecksumSHA256, ErrorCode: value.ErrorCode,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, StartedAt: value.StartedAt,
		FinishedAt: value.FinishedAt, ExpiresAt: value.ExpiresAt,
	}
}

func mapPresigned(value storage.PresignedRequest) *presignedRequestDTO {
	return &presignedRequestDTO{
		URL: value.URL, Method: value.Method, Headers: map[string][]string(value.Headers), ExpiresAt: value.ExpiresAt,
	}
}
