package store

import "github.com/pkg/errors"

var (
	ErrServerNotFound     = errors.New("server not found")
	ErrConditionExists    = errors.New("condition exists on server")
	ErrConditionNotFound  = errors.New("condition not found")
	ErrMalformedCondition = errors.New("condition data is invalid")
)
