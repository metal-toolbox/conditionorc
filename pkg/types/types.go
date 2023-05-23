package types

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.hollow.sh/toolbox/events"

	"golang.org/x/exp/slices"
)

// TODO: move into a shared package

// ConditionKind defines model for ConditionKind.
type ConditionKind string

// EventUrnNamespace is the namespace value set on a stream event.
//
// TODO: move into hollow-toolbox/events package.
type EventUrnNamespace string

const (
	FirmwareInstallOutofband ConditionKind = "firmwareInstallOutofband"
	InventoryOutofband       ConditionKind = "inventoryOutofband"
	ServerResourceType       string        = "servers"
	ConditionResourceType    string        = "condition"

	ServerserviceNamespace EventUrnNamespace = "hollow-serverservice"
	ControllerUrnNamespace EventUrnNamespace = "hollow-controllers"

	ConditionCreateEvent  events.EventType = "create"
	ConditionRequestEvent events.EventType = "request"
	ConditionUpdateEvent  events.EventType = "update"

	// ConditionStructVersion identifies the condition struct revision
	ConditionStructVersion string = "1"
)

func (k ConditionKind) EventType() string {
	switch k {
	case FirmwareInstallOutofband:
		return fmt.Sprintf("firmware.%s", FirmwareInstallOutofband)
	case InventoryOutofband:
		return fmt.Sprintf("inventory.%s", InventoryOutofband)
	default:
		return string(k)
	}
}

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
	// Version identifies the revision number for this struct.
	Version string `json:"version"`

	// ID is the identifier for this condition.
	ID uuid.UUID `json:"id"`

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

	// Fault is used to introduce faults into the controller when executing on a condition.
	Fault *Fault `json:"fault,omitempty"`

	// ResourceVersion has to be set to the value received by the
	// client updating it, this it to make sure condition updates
	// occur in the expected order.
	ResourceVersion int64 `json:"resourceVersion"`

	// UpdatedAt is when this object was last updated.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`

	// CreatedAt is when this object was created.
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// Fault is used to introduce faults into the controller when executing on a condition.
//
// Note: this depends on controllers implementing support to honor the given fault.
type Fault struct {
	//  will cause the condition execution to panic on the controller.
	Panic bool `json:"panic"`

	// Introduce specified delay in execution of the condition on the controller.
	ExecuteWithDelay time.Duration `json:"executionWithDelay,omitempty"`

	// FailAt is a controller specific task/stage that the condition should fail in execution.
	//
	// for example, in the flasher controller, setting this field to `init` will cause the
	// condition task to fail at initialization.
	FailAt string `json:"failAt,omitempty"`
}

// MustBytes returns an encoded json representation of the condition or panics
func (c *Condition) MustBytes() []byte {
	byt, err := json.Marshal(c)
	if err != nil {
		panic("encoding condition failed: " + err.Error())
	}
	return byt
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
