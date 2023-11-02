package fleetdb

import "github.com/pkg/errors"

var (
	ErrBMCCredentials = errors.New("invalid bmc credentials. missing user or password")
)
