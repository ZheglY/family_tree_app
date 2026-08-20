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
	"github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/ZheglY/family_tree_app/internal/features/trees/service"
	"github.com/google/uuid"
)

type TreeService interface {
	Create(context.Context, service.CreateCommand) (domain.TreeAccess, error)
	List(context.Context, uuid.UUID) ([]domain.TreeAccess, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (domain.TreeAccess, error)
	Update(context.Context, service.UpdateCommand) (domain.TreeAccess, error)
	Delete(context.Context, service.MutationCommand) error
	Restore(context.Context, service.MutationCommand) (domain.TreeAccess, error)
}

type Handler struct {
	service       TreeService
	requireAccess coremiddleware.Middleware
}

func NewHandler(
	service TreeService,
	requireAccess coremiddleware.Middleware,
) *Handler {
	return &Handler{service: service, requireAccess: requireAccess}
}

func (h *Handler) Routes() []server.Route {
	protected := []coremiddleware.Middleware{h.requireAccess}
	return []server.Route{
		{Method: http.MethodPost, Path: "/trees", Handler: h.Create, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees", Handler: h.List, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}", Handler: h.Get, Middleware: protected},
		{Method: http.MethodPatch, Path: "/trees/{tree_id}", Handler: h.Update, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}", Handler: h.Delete, Middleware: protected},
		{Method: http.MethodPost, Path: "/trees/{tree_id}/restore", Handler: h.Restore, Middleware: protected},
	}
}

type createRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
}

type updateRequest struct {
	Version     int     `json:"version"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type treeDTO struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	OwnerUserID  uuid.UUID  `json:"owner_user_id"`
	RootPersonID *uuid.UUID `json:"root_person_id,omitempty"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	Privacy      string     `json:"privacy"`
	Locale       string     `json:"locale"`
	Timezone     string     `json:"timezone"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	Version      int        `json:"version"`
}

type accessDTO struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type treeResponse struct {
	Tree   treeDTO   `json:"tree"`
	Access accessDTO `json:"access"`
}

type listResponse struct {
	Items []treeResponse `json:"items"`
}

func (h *Handler) Create(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, ok := authenticatedPrincipal(rw, httpRequest)
	if !ok {
		return
	}
	var body createRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	created, err := h.service.Create(httpRequest.Context(), service.CreateCommand{
		ActorUserID: principal.UserID,
		Name:        body.Name,
		Description: body.Description,
		Locale:      body.Locale,
		Timezone:    body.Timezone,
	})
	if err != nil {
		writeTreeError(rw, httpRequest, err, "Family tree could not be created")
		return
	}
	writeJSON(rw, httpRequest, mapTreeResponse(created), http.StatusCreated)
}

func (h *Handler) List(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, ok := authenticatedPrincipal(rw, httpRequest)
	if !ok {
		return
	}
	trees, err := h.service.List(httpRequest.Context(), principal.UserID)
	if err != nil {
		writeTreeError(rw, httpRequest, err, "Family trees could not be loaded")
		return
	}
	items := make([]treeResponse, 0, len(trees))
	for _, tree := range trees {
		items = append(items, mapTreeResponse(tree))
	}
	writeJSON(rw, httpRequest, listResponse{Items: items}, http.StatusOK)
}

func (h *Handler) Get(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTreeID(rw, httpRequest)
	if !ok {
		return
	}
	tree, err := h.service.Get(httpRequest.Context(), principal.UserID, treeID)
	if err != nil {
		writeTreeError(rw, httpRequest, err, "Family tree could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapTreeResponse(tree), http.StatusOK)
}

func (h *Handler) Update(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTreeID(rw, httpRequest)
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
		Version:     body.Version,
		Name:        body.Name,
		Description: body.Description,
		Locale:      body.Locale,
		Timezone:    body.Timezone,
	})
	if err != nil {
		writeTreeError(rw, httpRequest, err, "Family tree could not be updated")
		return
	}
	writeJSON(rw, httpRequest, mapTreeResponse(updated), http.StatusOK)
}

func (h *Handler) Delete(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTreeID(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	if err := h.service.Delete(
		httpRequest.Context(),
		mutationCommand(httpRequest, principal.UserID, treeID, body.Version),
	); err != nil {
		writeTreeError(rw, httpRequest, err, "Family tree could not be deleted")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Restore(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTreeID(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	restored, err := h.service.Restore(
		httpRequest.Context(),
		mutationCommand(httpRequest, principal.UserID, treeID, body.Version),
	)
	if err != nil {
		writeTreeError(rw, httpRequest, err, "Family tree could not be restored")
		return
	}
	writeJSON(rw, httpRequest, mapTreeResponse(restored), http.StatusOK)
}

func authenticatedPrincipal(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, bool) {
	principal, ok := access.PrincipalFromContext(httpRequest.Context())
	if !ok {
		response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
			ErrorResponse(apperrors.ErrUnauthorized, "Authentication is required")
	}
	return principal, ok
}

func principalAndTreeID(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, bool) {
	principal, ok := authenticatedPrincipal(rw, httpRequest)
	if !ok {
		return access.Principal{}, uuid.Nil, false
	}
	treeID, err := uuid.Parse(httpRequest.PathValue("tree_id"))
	if err != nil || treeID == uuid.Nil {
		writeTreeError(rw, httpRequest, domain.ErrTreeNotFound, "Family tree was not found")
		return access.Principal{}, uuid.Nil, false
	}
	return principal, treeID, true
}

func mutationCommand(
	httpRequest *http.Request,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	version int,
) service.MutationCommand {
	return service.MutationCommand{
		ActorUserID: actorUserID,
		TreeID:      treeID,
		Version:     version,
		RequestID:   requestid.FromContext(httpRequest.Context()),
		IPAddress:   directIPAddress(httpRequest),
	}
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
		writeTreeError(
			rw,
			httpRequest,
			fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err),
			"Request body is invalid",
		)
		return false
	}
	return true
}

func writeTreeError(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, domain.ErrInvalidTree):
		err = fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err)
	case errors.Is(err, domain.ErrTreeNotFound):
		err = fmt.Errorf("%w: %v", apperrors.ErrNotFound, err)
	case errors.Is(err, domain.ErrTreeAccessDenied):
		err = fmt.Errorf("%w: %v", apperrors.ErrForbidden, err)
	case errors.Is(err, domain.ErrTreeVersionConflict),
		errors.Is(err, domain.ErrTreeNotDeleted):
		err = fmt.Errorf("%w: %v", apperrors.ErrConflict, err)
	}
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		ErrorResponse(err, message)
}

func writeJSON(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	body any,
	statusCode int,
) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		JSONResponse(body, statusCode)
}

func mapTreeResponse(access domain.TreeAccess) treeResponse {
	tree := access.Tree
	return treeResponse{
		Tree: treeDTO{
			ID:           tree.ID,
			Name:         tree.Name,
			Description:  tree.Description,
			OwnerUserID:  tree.OwnerUserID,
			RootPersonID: tree.RootPersonID,
			CoverMediaID: tree.CoverMediaID,
			Privacy:      tree.Privacy,
			Locale:       tree.Locale,
			Timezone:     tree.Timezone,
			CreatedAt:    tree.CreatedAt,
			UpdatedAt:    tree.UpdatedAt,
			DeletedAt:    tree.DeletedAt,
			Version:      tree.Version,
		},
		Access: accessDTO{
			Role:   access.Membership.Role,
			Status: access.Membership.Status,
		},
	}
}
