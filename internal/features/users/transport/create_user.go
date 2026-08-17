package usershttp

import "net/http"

type CreateUserRequest struct {
	FirstName  string `json:"full_name" validate:"required,min=2,max=30"`
	SecondName string `json:"second_name" validate:"required,min=2,max=30"`
}

func (h *UsersHTTPHandler) CreateUser(rw http.ResponseWriter, r *http.Request) {

}
