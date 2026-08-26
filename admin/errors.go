package admin

import "errors"

var (
	errUnauthorized = errors.New("unauthorized")
	errExists       = errors.New("already exists")
	errBadTSIGName  = errors.New("invalid key name")
)
