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
	"github.com/ZheglY/family_tree_app/internal/features/relationships/domain"
	"github.com/ZheglY/family_tree_app/internal/features/relationships/service"
	"github.com/google/uuid"
)

const defaultGraphDepth = 2

type RelationshipService interface {
	Create(context.Context, service.CreateCommand) (service.Result, error)
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (service.Result, error)
	Update(context.Context, service.UpdateCommand) (service.Result, error)
	Delete(context.Context, service.MutationCommand) error
	Graph(context.Context, service.GraphCommand) (service.GraphResult, error)
}

type Handler struct {
	service       RelationshipService
	requireAccess coremiddleware.Middleware
}

func NewHandler(service RelationshipService, requireAccess coremiddleware.Middleware) *Handler {
	return &Handler{service: service, requireAccess: requireAccess}
}

func (h *Handler) Routes() []server.Route {
	protected := []coremiddleware.Middleware{h.requireAccess}
	return []server.Route{
		{
			Method: http.MethodPost, Path: "/trees/{tree_id}/parent-child-relations",
			Handler: h.Create, Middleware: protected,
		},
		{
			Method: http.MethodGet, Path: "/trees/{tree_id}/parent-child-relations/{relation_id}",
			Handler: h.Get, Middleware: protected,
		},
		{
			Method: http.MethodPatch, Path: "/trees/{tree_id}/parent-child-relations/{relation_id}",
			Handler: h.Update, Middleware: protected,
		},
		{
			Method: http.MethodDelete, Path: "/trees/{tree_id}/parent-child-relations/{relation_id}",
			Handler: h.Delete, Middleware: protected,
		},
		{
			Method: http.MethodGet, Path: "/trees/{tree_id}/graph",
			Handler: h.Graph, Middleware: protected,
		},
	}
}

type createRequest struct {
	ParentPersonID uuid.UUID `json:"parent_person_id"`
	ChildPersonID  uuid.UUID `json:"child_person_id"`
	RelationType   string    `json:"relation_type"`
	Confidence     string    `json:"confidence"`
	Note           string    `json:"note"`
}

type updateRequest struct {
	Version      int     `json:"version"`
	RelationType *string `json:"relation_type"`
	Confidence   *string `json:"confidence"`
	Note         *string `json:"note"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type relationDTO struct {
	ID             uuid.UUID  `json:"id"`
	TreeID         uuid.UUID  `json:"tree_id"`
	ParentPersonID uuid.UUID  `json:"parent_person_id"`
	ChildPersonID  uuid.UUID  `json:"child_person_id"`
	RelationType   string     `json:"relation_type"`
	Confidence     string     `json:"confidence"`
	Note           string     `json:"note"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	Version        int        `json:"version"`
}

type preferredNameDTO struct {
	ID           uuid.UUID `json:"id"`
	GivenName    string    `json:"given_name"`
	Patronymic   string    `json:"patronymic"`
	FamilyName   string    `json:"family_name"`
	Prefix       string    `json:"prefix"`
	Suffix       string    `json:"suffix"`
	FullText     string    `json:"full_text"`
	LanguageCode string    `json:"language_code"`
}

type personSummaryDTO struct {
	ID             uuid.UUID        `json:"id"`
	Sex            string           `json:"sex"`
	LifeStatus     string           `json:"life_status"`
	PrimaryMediaID *uuid.UUID       `json:"primary_media_id,omitempty"`
	PreferredName  preferredNameDTO `json:"preferred_name"`
}

type accessDTO struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type relationResponse struct {
	Relation relationDTO `json:"relation"`
	Access   accessDTO   `json:"access"`
}

type graphResponse struct {
	CenterPersonID       uuid.UUID          `json:"center_person_id"`
	Persons              []personSummaryDTO `json:"persons"`
	ParentChildRelations []relationDTO      `json:"parent_child_relations"`
	Unions               []struct{}         `json:"unions"`
	UnionMembers         []struct{}         `json:"union_members"`
	IncludePartners      bool               `json:"include_partners"`
	Access               accessDTO          `json:"access"`
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
			ParentPersonID: body.ParentPersonID,
			ChildPersonID:  body.ChildPersonID,
			RelationType:   body.RelationType,
			Confidence:     body.Confidence,
			Note:           body.Note,
		},
	})
	if err != nil {
		writeRelationshipError(rw, httpRequest, err, "Parent-child relation could not be created")
		return
	}
	writeJSON(rw, httpRequest, mapResult(created), http.StatusCreated)
}

func (h *Handler) Get(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, relationID, ok := principalTreeAndRelation(rw, httpRequest)
	if !ok {
		return
	}
	result, err := h.service.Get(httpRequest.Context(), principal.UserID, treeID, relationID)
	if err != nil {
		writeRelationshipError(rw, httpRequest, err, "Parent-child relation could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapResult(result), http.StatusOK)
}

func (h *Handler) Update(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, relationID, ok := principalTreeAndRelation(rw, httpRequest)
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
		RelationID:  relationID,
		Version:     body.Version,
		Values: domain.UpdateValues{
			RelationType: body.RelationType,
			Confidence:   body.Confidence,
			Note:         body.Note,
		},
	})
	if err != nil {
		writeRelationshipError(rw, httpRequest, err, "Parent-child relation could not be updated")
		return
	}
	writeJSON(rw, httpRequest, mapResult(updated), http.StatusOK)
}

func (h *Handler) Delete(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, relationID, ok := principalTreeAndRelation(rw, httpRequest)
	if !ok {
		return
	}
	var body versionRequest
	if !decodeBody(rw, httpRequest, &body) {
		return
	}
	err := h.service.Delete(
		httpRequest.Context(),
		mutationCommand(httpRequest, principal.UserID, treeID, relationID, body.Version),
	)
	if err != nil {
		writeRelationshipError(rw, httpRequest, err, "Parent-child relation could not be deleted")
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Graph(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return
	}
	command, err := graphCommand(httpRequest, principal.UserID, treeID)
	if err != nil {
		writeRelationshipError(rw, httpRequest, err, "Graph parameters are invalid")
		return
	}
	result, err := h.service.Graph(httpRequest.Context(), command)
	if err != nil {
		writeRelationshipError(rw, httpRequest, err, "Family graph could not be loaded")
		return
	}
	writeJSON(rw, httpRequest, mapGraphResult(result), http.StatusOK)
}

func graphCommand(
	httpRequest *http.Request,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (service.GraphCommand, error) {
	query := httpRequest.URL.Query()
	centerPersonID, err := uuid.Parse(query.Get("center_person_id"))
	if err != nil || centerPersonID == uuid.Nil {
		return service.GraphCommand{}, domain.ErrInvalidRelation
	}
	ancestorsDepth, err := queryInteger(query.Get("ancestors_depth"), defaultGraphDepth)
	if err != nil {
		return service.GraphCommand{}, err
	}
	descendantsDepth, err := queryInteger(query.Get("descendants_depth"), defaultGraphDepth)
	if err != nil {
		return service.GraphCommand{}, err
	}
	includePartners := false
	if value := query.Get("include_partners"); value != "" {
		includePartners, err = strconv.ParseBool(value)
		if err != nil {
			return service.GraphCommand{}, domain.ErrInvalidRelation
		}
	}
	return service.GraphCommand{
		ActorUserID:      actorUserID,
		TreeID:           treeID,
		CenterPersonID:   centerPersonID,
		AncestorsDepth:   ancestorsDepth,
		DescendantsDepth: descendantsDepth,
		IncludePartners:  includePartners,
	}, nil
}

func queryInteger(value string, defaultValue int) (int, error) {
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, domain.ErrInvalidRelation
	}
	return parsed, nil
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
		writeRelationshipError(rw, httpRequest, domain.ErrRelationNotFound, "Relationship was not found")
		return access.Principal{}, uuid.Nil, false
	}
	return principal, treeID, true
}

func principalTreeAndRelation(
	rw http.ResponseWriter,
	httpRequest *http.Request,
) (access.Principal, uuid.UUID, uuid.UUID, bool) {
	principal, treeID, ok := principalAndTree(rw, httpRequest)
	if !ok {
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	relationID, err := uuid.Parse(httpRequest.PathValue("relation_id"))
	if err != nil || relationID == uuid.Nil {
		writeRelationshipError(rw, httpRequest, domain.ErrRelationNotFound, "Relationship was not found")
		return access.Principal{}, uuid.Nil, uuid.Nil, false
	}
	return principal, treeID, relationID, true
}

func mutationCommand(
	httpRequest *http.Request,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	relationID uuid.UUID,
	version int,
) service.MutationCommand {
	return service.MutationCommand{
		ActorUserID: actorUserID,
		TreeID:      treeID,
		RelationID:  relationID,
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
		writeRelationshipError(
			rw,
			httpRequest,
			fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err),
			"Request body is invalid",
		)
		return false
	}
	return true
}

func writeRelationshipError(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, domain.ErrInvalidRelation):
		err = fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err)
	case errors.Is(err, domain.ErrRelationNotFound):
		err = fmt.Errorf("%w: %v", apperrors.ErrNotFound, err)
	case errors.Is(err, domain.ErrRelationAccessDenied):
		err = fmt.Errorf("%w: %v", apperrors.ErrForbidden, err)
	case errors.Is(err, domain.ErrRelationVersionConflict),
		errors.Is(err, domain.ErrDuplicateRelation):
		err = fmt.Errorf("%w: %v", apperrors.ErrConflict, err)
	case errors.Is(err, domain.ErrRelationCycle),
		errors.Is(err, domain.ErrGraphLimitExceeded):
		err = fmt.Errorf("%w: %v", apperrors.ErrUnprocessable, err)
	}
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		ErrorResponse(err, message)
}

func writeJSON(rw http.ResponseWriter, httpRequest *http.Request, body any, status int) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		JSONResponse(body, status)
}

func mapResult(result service.Result) relationResponse {
	return relationResponse{
		Relation: mapRelation(result.Relation),
		Access: accessDTO{
			Role:   result.Membership.Role,
			Status: result.Membership.Status,
		},
	}
}

func mapGraphResult(result service.GraphResult) graphResponse {
	persons := make([]personSummaryDTO, 0, len(result.Graph.Persons))
	for _, person := range result.Graph.Persons {
		persons = append(persons, personSummaryDTO{
			ID:             person.ID,
			Sex:            person.Sex,
			LifeStatus:     person.LifeStatus,
			PrimaryMediaID: person.PrimaryMediaID,
			PreferredName: preferredNameDTO{
				ID:           person.PreferredName.ID,
				GivenName:    person.PreferredName.GivenName,
				Patronymic:   person.PreferredName.Patronymic,
				FamilyName:   person.PreferredName.FamilyName,
				Prefix:       person.PreferredName.Prefix,
				Suffix:       person.PreferredName.Suffix,
				FullText:     person.PreferredName.FullText,
				LanguageCode: person.PreferredName.LanguageCode,
			},
		})
	}
	relations := make([]relationDTO, 0, len(result.Graph.Relations))
	for _, relation := range result.Graph.Relations {
		relations = append(relations, mapRelation(relation))
	}
	return graphResponse{
		CenterPersonID:       result.Graph.CenterPersonID,
		Persons:              persons,
		ParentChildRelations: relations,
		Unions:               make([]struct{}, 0),
		UnionMembers:         make([]struct{}, 0),
		IncludePartners:      result.IncludePartners,
		Access: accessDTO{
			Role:   result.Membership.Role,
			Status: result.Membership.Status,
		},
	}
}

func mapRelation(relation domain.ParentChildRelation) relationDTO {
	return relationDTO{
		ID:             relation.ID,
		TreeID:         relation.TreeID,
		ParentPersonID: relation.ParentPersonID,
		ChildPersonID:  relation.ChildPersonID,
		RelationType:   relation.RelationType,
		Confidence:     relation.Confidence,
		Note:           relation.Note,
		CreatedBy:      relation.CreatedBy,
		CreatedAt:      relation.CreatedAt,
		UpdatedAt:      relation.UpdatedAt,
		DeletedAt:      relation.DeletedAt,
		Version:        relation.Version,
	}
}
