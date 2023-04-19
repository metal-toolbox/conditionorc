package types

import (
	"encoding/json"

	"github.com/google/uuid"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
)

var (
	errBadUpdateTarget         error = errors.New("no existing condition found for update")
	errResourceVersionMismatch error = errors.New("resource version mismatch, retry request with current resourceVersion")
	errInvalidStateTransition  error = errors.New("invalid state transition")
)

type ServerResponse struct {
	StatusCode int                 `json:"statusCode,omitempty"`
	Message    string              `json:"message,omitempty"`
	Records    *ConditionsResponse `json:"records,omitempty"`
}

// ConditionsResponse is the response returned for listing multiple conditions on a server.
type ConditionsResponse struct {
	ServerID   uuid.UUID           `json:"serverID,omitempty"`
	Conditions []*ptypes.Condition `json:"conditions,omitempty"`
}

// ConditionCreate is the request payload to create a condition with its parameters on server.
type ConditionCreate struct {
	Exclusive  bool            `json:"exclusive"`
	Parameters json.RawMessage `json:"parameters"`
}

// NewCondition returns a new Condition type.
func (c *ConditionCreate) NewCondition(kind ptypes.ConditionKind) *ptypes.Condition {
	return &ptypes.Condition{
		ID:         uuid.New(),
		Version:    ptypes.ConditionStructVersion,
		Kind:       kind,
		State:      ptypes.Pending,
		Exclusive:  c.Exclusive,
		Parameters: c.Parameters,
	}
}

// ConditionUpdate is the request payload to update an existing condition.
type ConditionUpdate struct {
	State           ptypes.ConditionState `json:"state,omitempty"`
	Status          json.RawMessage       `json:"status,omitempty"`
	ResourceVersion int64                 `json:"resourceVersion"`
}

// MergeExisting when given an existing condition, validates the update based on existing values
// and returns a condition that can be passed to the repository for update.
//
// The resourceVersion is not updated here and is left for the repository Store to update.
//
// This method makes sure that update does not overwrite existing data inadvertently.
func (c *ConditionUpdate) MergeExisting(existing *ptypes.Condition) (*ptypes.Condition, error) {
	// 1. condition must already exist for update.
	if existing == nil {
		return nil, errBadUpdateTarget
	}

	// resourceVersion must match
	if existing.ResourceVersion != c.ResourceVersion {
		return nil, errResourceVersionMismatch
	}

	// transition is valid
	if !existing.State.TransitionValid(c.State) {
		return nil, errInvalidStateTransition
	}

	return &ptypes.Condition{
		Version:               existing.Version,
		ID:                    existing.ID,
		Kind:                  existing.Kind,
		Parameters:            existing.Parameters,
		State:                 c.State,
		Status:                c.Status,
		FailOnCheckpointError: existing.FailOnCheckpointError,
		Exclusive:             existing.Exclusive,
		ResourceVersion:       existing.ResourceVersion,
		UpdatedAt:             existing.UpdatedAt,
		CreatedAt:             existing.CreatedAt,
	}, nil
}
