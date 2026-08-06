package usershttp

import (
	"context"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/domain"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
)

type UsersHTTPHandler struct {
	userService UserService
}

type UserService interface {
	GetUser(
		ctx context.Context,
		id int,
	) (domain.User, error)
}

func (h *UsersHTTPHandler) Routes() []server.Route {
	return []server.Route{
		{
			Method: http.MethodPost,
			Path: "users/",
			Handler: h.CreateUser,
		},
		{
			Method: http.MethodGet,
			Path: "users/{id}",
			Handler: h.GetUser,
		},
	}
}