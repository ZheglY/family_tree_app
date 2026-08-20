package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/request"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/unions/domain"
	"github.com/ZheglY/family_tree_app/internal/features/unions/service"
	"github.com/google/uuid"
)

type UnionService interface {
	Create(context.Context, service.CreateCommand) (service.Result, error)
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (service.Result, error)
	Update(context.Context, service.UpdateCommand) (service.Result, error)
	Delete(context.Context, service.MutationCommand) error
	AddMember(context.Context, service.AddMemberCommand) (service.Result, error)
	RemoveMember(context.Context, service.RemoveMemberCommand) (service.Result, error)
}

type Handler struct {
	service       UnionService
	requireAccess coremiddleware.Middleware
}

func NewHandler(service UnionService, requireAccess coremiddleware.Middleware) *Handler {
	return &Handler{service: service, requireAccess: requireAccess}
}

func (h *Handler) Routes() []server.Route {
	protected := []coremiddleware.Middleware{h.requireAccess}
	return []server.Route{
		{Method: http.MethodPost, Path: "/trees/{tree_id}/unions", Handler: h.Create, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/unions/{union_id}", Handler: h.Get, Middleware: protected},
		{Method: http.MethodPatch, Path: "/trees/{tree_id}/unions/{union_id}", Handler: h.Update, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}/unions/{union_id}", Handler: h.Delete, Middleware: protected},
		{Method: http.MethodPost, Path: "/trees/{tree_id}/unions/{union_id}/members", Handler: h.AddMember, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}/unions/{union_id}/members/{person_id}", Handler: h.RemoveMember, Middleware: protected},
	}
}

type memberRequest struct {
	PersonID uuid.UUID `json:"person_id"`
	Role     string    `json:"role"`
}

type createRequest struct {
	Type      string          `json:"type"`
	EndReason string          `json:"end_reason"`
	Note      string          `json:"note"`
	Members   []memberRequest `json:"members"`
}

type updateRequest struct {
	Version   int     `json:"version"`
	Type      *string `json:"type"`
	EndReason *string `json:"end_reason"`
	Note      *string `json:"note"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type unionDTO struct {
	ID        uuid.UUID  `json:"id"`
	TreeID    uuid.UUID  `json:"tree_id"`
	Type      string     `json:"type"`
	EndReason string     `json:"end_reason"`
	Note      string     `json:"note"`
	CreatedBy uuid.UUID  `json:"created_by"`
	UpdatedBy uuid.UUID  `json:"updated_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Version   int        `json:"version"`
}

type memberDTO struct {
	PersonID  uuid.UUID `json:"person_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type accessDTO struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type unionResponse struct {
	Union   unionDTO    `json:"union"`
	Members []memberDTO `json:"members"`
	Access  accessDTO   `json:"access"`
}

func (h *Handler) Create(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	var body createRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	members := make([]domain.MemberValues, 0, len(body.Members))
	for _, member := range body.Members {
		members = append(members, domain.MemberValues{
			PersonID: member.PersonID,
			Role:     member.Role,
		})
	}
	created, err := h.service.Create(httpRequest.Context(), service.CreateCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		Values: domain.CreateValues{
			Type:      body.Type,
			EndReason: body.EndReason,
			Note:      body.Note,
			Members:   members,
		},
	})
	if err != nil {
		writeUnionError(rw, httpRequest, err, "Family union could not be created")
		return
	}
	writeJSON(rw, httpRequest, mapResult(created), http.StatusCreated)
}

func (h *Handler) Get(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, unionID, ok := principalTreeAndUnion(rw, httpRequest)
	if !ok {
		return
	}
	result, err := h.service.Get(httpRequest.Context(), principal.UserID, treeID, unionID)
	if err != nil {
		writeUnionError(rw, httpRequest, err, "Family union could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) Update(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, unionID, ok := principalTreeAndUnion(rw, httpRequest)
	if !ok {
		return
	}
	var body updateRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	updated, err := h.service.Update(httpRequest.Context(), service.UpdateCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		UnionID:     unionID,
		Version:     body.Version,
		Values: domain.UpdateValues{
			Type:      body.Type,
			EndReason: body.EndReason,
			Note:      body.Note,
		},
	})
	if err != nil {
		writeUnionError(rw, httpRequest, err, "Family union could not be updated")
		return
	}
	writeJSON(rw, httpRequest, mapResult(updated), http.StatusOK)
}

func (h *Handler) Delete(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, unionID, ok := principalTreeAndUnion(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	err := h.service.Delete(
		httpRequest.Context(),
		service.MutationCommand{
			ActorUserID: principal.UserID,
			TreeID:      treeID,
			UnionID:     unionID,
			Version:     body.Version,
			RequestID:   requestid.FromContext(httpRequest.Context()),
			IPAddress:   directIPAddress(httpRequest),
		},
	)
	if err != nil {
		writeUnionError(rw, httpRequest, err, "Family union could not be deleted")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddMember(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, unionID, ok := principalTreeAndUnion(rw, httpRequest)
	if !ok {
		return
	}
	var body memberRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	result, err := h.service.AddMember(httpRequest.Context(), service.AddMemberCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		UnionID:     unionID,
		PersonID:    body.PersonID,
		Role:        body.Role,
	})
	if err != nil {
		writeUnionError(rw, httpRequest, err, "Union member could not be added")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) RemoveMember(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, unionID, ok := principalTreeAndUnion(rw, httpRequest)
	if !ok {
		return
	}
	personID, err := uuid.Parse(httpRequest.PathValue("person_id"))
	if err != nil || personID == uuid.Nil {
		writeUnionError(rw, httpRequest, domain.ErrUnionMemberNotFound, "Union member was not found")
		return
	}
	result, err := h.service.RemoveMember(httpRequest.Context(), service.RemoveMemberCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		UnionID:     unionID,
		PersonID:    personID,
	})
	if err != nil {
		writeUnionError(rw, httpRequest, err, "Union member could not be removed")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
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
		writeUnionError(rw, httpRequest, domain.ErrUnionNotFound, "Family union was not found")
		return access.Principal{}, uuid.Nil, false
	}
	return principal, treeID, true
}

func principalTreeAndUnion(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, uuid.UUID, bool) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	unionID, err := uuid.Parse(httpRequest.PathValue("union_id"))
	if err != nil || unionID == uuid.Nil {
		writeUnionError(rw, httpRequest, domain.ErrUnionNotFound, "Family union was not found")
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	return principal, treeID, unionID, true
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

func decodeBody(rw http.ResponseWriter, httpRequest *http.Request, destination any) bool {
	if err := request.DecodeJSON(httpRequest.Body, destination); err != nil {
		writeUnionError(
			rw,
			httpRequest,
			fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err),
			"Request body is invalid",
		)
		return false
	}
	return true
}

func writeUnionError(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, domain.ErrInvalidUnion):
		err = fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err)
	case errors.Is(err, domain.ErrUnionNotFound),
		errors.Is(err, domain.ErrUnionMemberNotFound):
		err = fmt.Errorf("%w: %v", apperrors.ErrNotFound, err)
	case errors.Is(err, domain.ErrUnionAccessDenied):
		err = fmt.Errorf("%w: %v", apperrors.ErrForbidden, err)
	case errors.Is(err, domain.ErrUnionVersionConflict),
		errors.Is(err, domain.ErrDuplicateUnionMember):
		err = fmt.Errorf("%w: %v", apperrors.ErrConflict, err)
	case errors.Is(err, domain.ErrUnionMemberLimit):
		err = fmt.Errorf("%w: %v", apperrors.ErrUnprocessable, err)
	}
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		ErrorResponse(err, message)
}

func writeJSON(rw http.ResponseWriter, httpRequest *http.Request, body any, status int) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		JSONResponse(body, status)
}

func mapResult(result service.Result) unionResponse {
	union := result.Aggregate.Union
	members := make([]memberDTO, 0, len(result.Aggregate.Members))
	for _, member := range result.Aggregate.Members {
		members = append(members, memberDTO{
			PersonID:  member.PersonID,
			Role:      member.Role,
			CreatedAt: member.CreatedAt,
		})
	}
	return unionResponse{
		Union: unionDTO{
			ID:        union.ID,
			TreeID:    union.TreeID,
			Type:      union.Type,
			EndReason: union.EndReason,
			Note:      union.Note,
			CreatedBy: union.CreatedBy,
			UpdatedBy: union.UpdatedBy,
			CreatedAt: union.CreatedAt,
			UpdatedAt: union.UpdatedAt,
			DeletedAt: union.DeletedAt,
			Version:   union.Version,
		},
		Members: members,
		Access: accessDTO{
			Role:   result.Membership.Role,
			Status: result.Membership.Status,
		},
	}
}
