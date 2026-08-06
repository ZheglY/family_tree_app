package domain

type User struct {
	ID int
	Version int
	FirstName string
	SecondName string
}

func NewUser(
	id int,
	version int,
	firstName string,
	secondName string,
) *User{
	return &User{
		ID: id,
		Version: version,
		FirstName: firstName,
		SecondName: secondName,
	}
}