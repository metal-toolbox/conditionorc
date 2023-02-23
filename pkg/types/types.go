package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"golang.org/x/exp/slices"
)

// TODO: move into a shared package

// ConditionKind defines model for ConditionKind.
type ConditionKind string

// ConditionKinds is the list of ConditionKinds loaded from the condition definitions.
type ConditionKinds []ConditionKind

// Valid checks if the condition kind is known.
func (v ConditionKinds) Valid(k ConditionKind) bool {
	return slices.Contains(v, k)
}

// ConditionState defines model for ConditionState.
type ConditionState string

// Defines values for ConditionState.
const (
	Active    ConditionState = "active"
	Failed    ConditionState = "failed"
	Pending   ConditionState = "pending"
	Succeeded ConditionState = "succeeded"
)

// Condition defines model for Condition.
type Condition struct {
	// Kind is one of ConditionKind.
	Kind ConditionKind `json:"kind,omitempty"`

	// Parameters is a JSON object that is agreed upon by the controller
	// reconciling the condition and the client requesting the condition.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// State is one of ConditionState
	State ConditionState `json:"state,omitempty"`

	// Status is a JSON object that is agreed upon by the controller
	// reconciling the condition and the client requesting the condition.
	Status json.RawMessage `json:"status,omitempty"`

	// OnSuccess execute another condition when defined
	//
	// This is left here to be implemented later
	// OnSuccess *Condition `json:"onSuccess,omitempty"`

	// Exclusive indicates this condition holds exclusive access to the device
	// and other conditions have to wait until this is in a finalized state.
	Exclusive bool `json:"exclusive,omitempty"`

	// ResourceVersion has to be set to the value received by the
	// client updating it, this it to make sure condition updates
	// occur in the expected order.
	ResourceVersion int64 `json:"resourceVersion,omitempty"`

	// UpdatedAt is when this object was last updated.
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`

	// CreatedAt is when this object was created.
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// Server defines model for Server.
type Server struct {
	ID         uuid.UUID    `json:"id"`
	Conditions *[]Condition `json:"conditions,omitempty"`
}

// ConditionDefinition type holds attributes of a condition definition.
type ConditionDefinition struct {
	Name      ConditionKind `mapstructure:"name"`
	Exclusive bool          `mapstructure:"exclusive"`
}
