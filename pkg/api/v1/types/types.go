package types

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
)

type ServerResponse struct {
	StatusCode int                 `json:"statusCode,omitempty"`
	Message    string              `json:"message,omitempty"`
	Record     *ConditionResponse  `json:"record,omitempty"`
	Records    *ConditionsResponse `json:"records,omitempty"`
}

// ConditionResponse is the response returned for a single condition request.
type ConditionResponse struct {
	ServerID  uuid.UUID         `json:"serverID,omitempty"`
	Condition *ptypes.Condition `json:"condition,omitempty"`
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
		return nil, errors.New("no existing condition found for update")
	}

	// resourceVersion must match
	if existing.ResourceVersion != c.ResourceVersion {
		return nil, errors.New("resource version mismatch, retry request with current resourceVersion")
	}

	// transition is valid
	if !existing.State.TransitionValid(c.State) {
		return nil,
			// nolint:goerr113 // error needs to be dynamic.
			fmt.Errorf(
				"transition from exiting state %s to new state %s is not allowed",
				existing.State,
				c.State)
	}

	return &ptypes.Condition{
		Kind:       existing.Kind,
		Parameters: existing.Parameters,
		Exclusive:  existing.Exclusive,
		State:      c.State,
		Status:     c.Status,
	}, nil
}
