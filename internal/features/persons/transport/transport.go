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
	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/request"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/persons/domain"
	"github.com/ZheglY/family_tree_app/internal/features/persons/service"
	"github.com/google/uuid"
)

type PersonService interface {
	Create(context.Context, service.CreateCommand) (service.Result, error)
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (service.Result, error)
	List(context.Context, service.ListCommand) (service.ListResult, error)
	Update(context.Context, service.UpdateCommand) (service.Result, error)
	Delete(context.Context, service.MutationCommand) error
	Restore(context.Context, service.MutationCommand) (service.Result, error)
}

type Handler struct {
	service       PersonService
	requireAccess coremiddleware.Middleware
}

func NewHandler(service PersonService, requireAccess coremiddleware.Middleware) *Handler {
	return &Handler{service: service, requireAccess: requireAccess}
}

func (h *Handler) Routes() []server.Route {
	protected := []coremiddleware.Middleware{h.requireAccess}
	return []server.Route{
		{Method: http.MethodPost, Path: "/trees/{tree_id}/persons", Handler: h.Create, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/persons", Handler: h.List, Middleware: protected},
		{Method: http.MethodGet, Path: "/trees/{tree_id}/persons/{person_id}", Handler: h.Get, Middleware: protected},
		{Method: http.MethodPatch, Path: "/trees/{tree_id}/persons/{person_id}", Handler: h.Update, Middleware: protected},
		{Method: http.MethodDelete, Path: "/trees/{tree_id}/persons/{person_id}", Handler: h.Delete, Middleware: protected},
		{Method: http.MethodPost, Path: "/trees/{tree_id}/persons/{person_id}/restore", Handler: h.Restore, Middleware: protected},
	}
}

type nameRequest struct {
	GivenName    string `json:"given_name"`
	Patronymic   string `json:"patronymic"`
	FamilyName   string `json:"family_name"`
	Prefix       string `json:"prefix"`
	Suffix       string `json:"suffix"`
	LanguageCode string `json:"language_code"`
}

type createRequest struct {
	Sex           string      `json:"sex"`
	LifeStatus    string      `json:"life_status"`
	Biography     string      `json:"biography"`
	Notes         string      `json:"notes"`
	PreferredName nameRequest `json:"preferred_name"`
}

type updateRequest struct {
	Version       int          `json:"version"`
	Sex           *string      `json:"sex"`
	LifeStatus    *string      `json:"life_status"`
	Biography     *string      `json:"biography"`
	Notes         *string      `json:"notes"`
	PreferredName *nameRequest `json:"preferred_name"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type personDTO struct {
	ID             uuid.UUID  `json:"id"`
	TreeID         uuid.UUID  `json:"tree_id"`
	Sex            string     `json:"sex"`
	LifeStatus     string     `json:"life_status"`
	Biography      string     `json:"biography"`
	Notes          string     `json:"notes"`
	PrimaryMediaID *uuid.UUID `json:"primary_media_id,omitempty"`
	PrivacyLevel   string     `json:"privacy_level"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	UpdatedBy      uuid.UUID  `json:"updated_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	Version        int        `json:"version"`
}

type nameDTO struct {
	ID           uuid.UUID `json:"id"`
	Type         string    `json:"type"`
	GivenName    string    `json:"given_name"`
	Patronymic   string    `json:"patronymic"`
	FamilyName   string    `json:"family_name"`
	Prefix       string    `json:"prefix"`
	Suffix       string    `json:"suffix"`
	FullText     string    `json:"full_text"`
	IsPreferred  bool      `json:"is_preferred"`
	LanguageCode string    `json:"language_code"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type accessDTO struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type personResponse struct {
	Person        personDTO `json:"person"`
	PreferredName nameDTO   `json:"preferred_name"`
	Access        accessDTO `json:"access"`
}

type listResponse struct {
	Items      []personResponse `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
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
	created, err := h.service.Create(httpRequest.Context(), service.CreateCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		Values: domain.CreateValues{
			Sex:        body.Sex,
			LifeStatus: body.LifeStatus,
			Biography:  body.Biography,
			Notes:      body.Notes,
			Name:       mapNameValues(body.PreferredName),
		},
	})
	if err != nil {
		writePersonError(rw, httpRequest, err, "Person could not be created")
		return
	}
	writeJSON(rw, httpRequest, mapResult(created), http.StatusCreated)
}

func (h *Handler) List(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	command, err := listCommand(httpRequest, principal.UserID, treeID)
	if err != nil {
		writePersonError(rw, httpRequest, err, "Person filters are invalid")
		return
	}
	result, err := h.service.List(httpRequest.Context(), command)
	if err != nil {
		writePersonError(rw, httpRequest, err, "Persons could not be loaded")
		return
	}
	items := make([]personResponse, 0, len(result.Items))
	for _, card := range result.Items {
		items = append(items, mapCard(card, result.Membership.Role, result.Membership.Status))
	}
	writeJSON(rw, httpRequest, listResponse{Items: items, NextCursor: result.NextCursor}, http.StatusOK)
}

func (h *Handler) Get(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	result, err := h.service.Get(httpRequest.Context(), principal.UserID, treeID, personID)
	if err != nil {
		writePersonError(rw, httpRequest, err, "Person could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) Update(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	var body updateRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	values := domain.UpdateValues{
		Sex:        body.Sex,
		LifeStatus: body.LifeStatus,
		Biography:  body.Biography,
		Notes:      body.Notes,
	}
	if body.PreferredName != nil {
		name := mapNameValues(*body.PreferredName)
		values.PreferredName = &name
	}
	updated, err := h.service.Update(httpRequest.Context(), service.UpdateCommand{
		ActorUserID: principal.UserID,
		TreeID:      treeID,
		PersonID:    personID,
		Version:     body.Version,
		Values:      values,
	})
	if err != nil {
		writePersonError(rw, httpRequest, err, "Person could not be updated")
		return
	}
	writeJSON(rw, httpRequest, mapResult(updated), http.StatusOK)
}

func (h *Handler) Delete(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	err := h.service.Delete(
		httpRequest.Context(),
		mutationCommand(httpRequest, principal.UserID, treeID, personID, body.Version),
	)
	if err != nil {
		writePersonError(rw, httpRequest, err, "Person could not be deleted")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Restore(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, personID, ok := principalTreeAndPerson(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	restored, err := h.service.Restore(
		httpRequest.Context(),
		mutationCommand(httpRequest, principal.UserID, treeID, personID, body.Version),
	)
	if err != nil {
		writePersonError(rw, httpRequest, err, "Person could not be restored")
		return
	}
	writeJSON(rw, httpRequest, mapResult(restored), http.StatusOK)
}

func listCommand(
	httpRequest *http.Request,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (service.ListCommand, error) {
	query := httpRequest.URL.Query()
	command := service.ListCommand{
		ActorUserID: actorUserID,
		TreeID:      treeID,
		Query:       query.Get("query"),
		LifeStatus:  query.Get("life_status"),
		Cursor:      query.Get("cursor"),
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return service.ListCommand{}, domain.ErrInvalidPerson
		}
		command.Limit = limit
	}
	if value := query.Get("has_media"); value != "" {
		hasMedia, err := strconv.ParseBool(value)
		if err != nil {
			return service.ListCommand{}, domain.ErrInvalidPerson
		}
		command.HasMedia = &hasMedia
	}
	return command, nil
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
		writePersonError(rw, httpRequest, domain.ErrPersonNotFound, "Person was not found")
		return access.Principal{}, uuid.Nil, false
	}
	return principal, treeID, true
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
		writePersonError(rw, httpRequest, domain.ErrPersonNotFound, "Person was not found")
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	return principal, treeID, personID, true
}

func mutationCommand(
	httpRequest *http.Request,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	personID uuid.UUID,
	version int,
) service.MutationCommand {
	return service.MutationCommand{
		ActorUserID: actorUserID,
		TreeID:      treeID,
		PersonID:    personID,
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
		writePersonError(
			rw,
			httpRequest,
			fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err),
			"Request body is invalid",
		)
		return false
	}
	return true
}

func writePersonError(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, domain.ErrInvalidPerson),
		errors.Is(err, domain.ErrInvalidListCursor):
		err = fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err)
	case errors.Is(err, domain.ErrPersonNotFound):
		err = fmt.Errorf("%w: %v", apperrors.ErrNotFound, err)
	case errors.Is(err, domain.ErrPersonAccessDenied):
		err = fmt.Errorf("%w: %v", apperrors.ErrForbidden, err)
	case errors.Is(err, domain.ErrPersonVersionConflict),
		errors.Is(err, domain.ErrPersonNotDeleted):
		err = fmt.Errorf("%w: %v", apperrors.ErrConflict, err)
	}
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		ErrorResponse(err, message)
}

func writeJSON(rw http.ResponseWriter, httpRequest *http.Request, body any, status int) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		JSONResponse(body, status)
}

func mapResult(result service.Result) personResponse {
	return mapCard(result.Card, result.Membership.Role, result.Membership.Status)
}

func mapCard(card domain.Card, role string, status string) personResponse {
	person := card.Person
	name := card.PreferredName
	return personResponse{
		Person: personDTO{
			ID:             person.ID,
			TreeID:         person.TreeID,
			Sex:            person.Sex,
			LifeStatus:     person.LifeStatus,
			Biography:      person.Biography,
			Notes:          person.Notes,
			PrimaryMediaID: person.PrimaryMediaID,
			PrivacyLevel:   person.PrivacyLevel,
			CreatedBy:      person.CreatedBy,
			UpdatedBy:      person.UpdatedBy,
			CreatedAt:      person.CreatedAt,
			UpdatedAt:      person.UpdatedAt,
			DeletedAt:      person.DeletedAt,
			Version:        person.Version,
		},
		PreferredName: nameDTO{
			ID:           name.ID,
			Type:         name.Type,
			GivenName:    name.GivenName,
			Patronymic:   name.Patronymic,
			FamilyName:   name.FamilyName,
			Prefix:       name.Prefix,
			Suffix:       name.Suffix,
			FullText:     name.FullText,
			IsPreferred:  name.IsPreferred,
			LanguageCode: name.LanguageCode,
			CreatedAt:    name.CreatedAt,
			UpdatedAt:    name.UpdatedAt,
		},
		Access: accessDTO{Role: role, Status: status},
	}
}

func mapNameValues(name nameRequest) domain.NameValues {
	return domain.NameValues{
		GivenName:    name.GivenName,
		Patronymic:   name.Patronymic,
		FamilyName:   name.FamilyName,
		Prefix:       name.Prefix,
		Suffix:       name.Suffix,
		LanguageCode: name.LanguageCode,
	}
}
