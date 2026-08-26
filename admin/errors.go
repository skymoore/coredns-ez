package admin

import "errors"

var (
	errUnauthorized = errors.New("unauthorized")
	errExists       = errors.New("already exists")
)
