package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.hollow.sh/toolbox/events"
	"golang.org/x/exp/slices"
)

// TODO: move into a shared package

// ConditionKind defines model for ConditionKind.
type ConditionKind string

const (
	FirmwareInstallOutofband ConditionKind = "firmwareInstallOutofband"
	InventoryOutofband       ConditionKind = "inventoryOutofband"
	ServerResourceType       string        = "servers"

	ConditionEventType events.EventType = "condition"
)

// ConditionState defines model for ConditionState.
type ConditionState string

// Defines values for ConditionState.
const (
	Pending   ConditionState = "pending"
	Active    ConditionState = "active"
	Failed    ConditionState = "failed"
	Succeeded ConditionState = "succeeded"
)

// ConditionStates returns available condition states.
func ConditionStates() []ConditionState {
	return []ConditionState{
		Active,
		Pending,
		Failed,
		Succeeded,
	}
}

// Transition valid returns a bool value if the state transition is allowed.
func (current ConditionState) TransitionValid(next ConditionState) bool {
	switch {
	// Pending state can stay in Pending or transition to Active or Failed or Succeeded
	case current == Pending && slices.Contains([]ConditionState{Pending, Active, Failed, Succeeded}, next):
		return true
	// Active state can stay in Active or transition to Failed or Succeeded
	case current == Active && slices.Contains([]ConditionState{Active, Failed, Succeeded}, next):
		return true
	default:
		return false
	}
}

// ConditionStateValid validates the ConditionState.
func ConditionStateIsValid(s ConditionState) bool {
	return slices.Contains(ConditionStates(), s)
}

// ConditionStateComplete returns true when the given state is considered to be final.
func ConditionStateIsComplete(s ConditionState) bool {
	return slices.Contains([]ConditionState{Failed, Succeeded}, s)
}

// Parameters is an interface for Condition Parameter types
type Parameters interface {
	Validate() error
}

// ConditionDefinition holds the default parameters for a Condition.
type ConditionDefinition struct {
	Kind                  ConditionKind `mapstructure:"kind"`
	Exclusive             bool          `mapstructure:"exclusive"`
	FailOnCheckpointError bool          `mapstructure:"failOnCheckpointError"`
	// TODO: we might want to be able to pass some parameters to the next condition.
	OnSuccessCondition ConditionKind `mapstructure:"onSuccessCondition"`
}

// ConditionDefinitions is the list of conditions with helper methods.
type ConditionDefinitions []*ConditionDefinition

func (c ConditionDefinitions) FindByKind(k ConditionKind) *ConditionDefinition {
	for _, e := range c {
		if e.Kind == k {
			return e
		}
	}

	return nil
}

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
	// OnSuccess *Condition `json:"onSuccess,omitempty"`

	// Should the worker executing this condition fail if its unable to checkpoint
	// the status of work on this condition.
	FailOnCheckpointError bool `json:"failOnCheckpointError,omitempty"`

	// Exclusive indicates this condition holds exclusive access to the device
	// and other conditions have to wait until this is in a finalized state.
	Exclusive bool `json:"exclusive,omitempty"`

	// ResourceVersion has to be set to the value received by the
	// client updating it, this it to make sure condition updates
	// occur in the expected order.
	ResourceVersion int64 `json:"resourceVersion"`

	// UpdatedAt is when this object was last updated.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`

	// CreatedAt is when this object was created.
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// StateValid validates the Condition State field.
func (c *Condition) StateValid() bool {
	return ConditionStateIsValid(c.State)
}

// IsComplete returns true if the condition has a state that is final.
func (c *Condition) IsComplete() bool {
	return ConditionStateIsComplete(c.State)
}

// ServerConditions is a type to hold a server ID and the conditions associated with it.
type ServerConditions struct {
	ServerID   uuid.UUID
	Conditions []*Condition
}
