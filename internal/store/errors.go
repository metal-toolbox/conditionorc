package store

import "github.com/pkg/errors"

var (
	ErrConditionExists      = errors.New("condition exists on server")
	ErrConditionNotFound    = errors.New("condition not found")
	ErrMalformedCondition   = errors.New("condition data is invalid")
	ErrNoCreate             = errors.New("unable to create condition")
	ErrConditionNotComplete = errors.New("condition is not complete") // can't delete
)
