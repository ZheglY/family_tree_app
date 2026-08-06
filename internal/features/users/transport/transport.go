package usershttp

import "context"

type UsersHTTPHandler struct {
}

type UserService interface {
	GetUser(
		ctx context.Context,
		id int,
	) ()
}
