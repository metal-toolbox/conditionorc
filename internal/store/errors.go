package store

import "github.com/pkg/errors"

var (
	ErrConditionExists    = errors.New("condition exists on server")
	ErrConditionNotFound  = errors.New("condition not found")
	ErrMalformedCondition = errors.New("condition data is invalid")
	ErrNoCreate           = errors.New("unable to create condition")
	ErrConditionComplete  = errors.New("condition is in a final state") // can't update
)
