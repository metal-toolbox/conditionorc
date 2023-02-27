package routes

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
)

type ServerResponse struct {
	Message string      `json:"message,omitempty"`
	Record  interface{} `json:"record,omitempty"`
	Records interface{} `json:"records,omitempty"`
}

// ConditionResponse is the response returned for a single condition request.
type ConditionResponse struct {
	ServerID  uuid.UUID         `json:"server_id"`
	Condition *ptypes.Condition `json:"condition,omitempty"`
}

// ConditionsResponse is the response returned for listing multiple conditions on a server.
type ConditionsResponse struct {
	ServerID   uuid.UUID           `json:"server_id"`
	Conditions []*ptypes.Condition `json:"conditions,omitempty"`
}

// ConditionCreate is the request payload to create a condition with its parameters on server.
type ConditionCreate struct {
	Parameters json.RawMessage `json:"parameters"`
}

// newCondition validates its field values and returns a Condition type.
func (c *ConditionCreate) newCondition(kind ptypes.ConditionKind) (*ptypes.Condition, error) {
	condition := &ptypes.Condition{
		Kind:  kind,
		State: ptypes.Pending,
	}

	switch kind {
	case ptypes.FirmwareInstallOutofband:
		parameters := &ptypes.FirmwareInstallOutofbandParameters{}

		if err := json.Unmarshal(c.Parameters, parameters); err != nil {
			return nil, errors.Wrap(ErrConditionParameter, err.Error())
		}

		if err := parameters.Validate(); err != nil {
			return nil, errors.Wrap(ErrConditionParameter, err.Error())
		}

		condition.Parameters = parameters

	case ptypes.InventoryOutofband:
		parameters := &ptypes.InventoryOutofbandParameters{}

		if err := json.Unmarshal(c.Parameters, parameters); err != nil {
			return nil, errors.Wrap(ErrConditionParameter, err.Error())
		}

		if err := parameters.Validate(); err != nil {
			return nil, errors.Wrap(ErrConditionParameter, err.Error())
		}

		condition.Parameters = parameters

	case ptypes.GenerateFirmwareSet:
		parameters := &ptypes.GenerateFirmwareSetParameters{}

		if err := json.Unmarshal(c.Parameters, parameters); err != nil {
			return nil, errors.Wrap(ErrConditionParameter, err.Error())
		}

		if err := parameters.Validate(); err != nil {
			return nil, errors.Wrap(ErrConditionParameter, err.Error())
		}

		condition.Parameters = parameters

	default:
		return nil, errors.Wrap(ErrConditionParameter, "unsupported conditionKind: "+string(kind))
	}

	return condition, nil
}

// ConditionUpdate is the request payload to update an existing condition.
type ConditionUpdate struct {
	State           ptypes.ConditionState `json:"state,omitempty"`
	Status          json.RawMessage       `json:"status,omitempty"`
	ResourceVersion int64                 `json:"resource_version"`
}

// mergeExisting when given an existing condition, validates the update based on existing values
// and returns a condition that can be passed to the repository for update.
//
// The resourceVersion is not updated here and is left for the repository Store to update.
//
// This method makes sure that update does not overwrite existing data inadvertently.
func (c *ConditionUpdate) mergeExisting(existing *ptypes.Condition) (*ptypes.Condition, error) {
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
