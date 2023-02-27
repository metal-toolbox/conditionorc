package types

import (
	"encoding/json"
	"time"

	"golang.org/x/exp/slices"
)

// TODO: move into a shared package

// ConditionKind defines model for ConditionKind.
type ConditionKind string

const (
	FirmwareInstallOutofband ConditionKind = "firmwareInstallOutofband"
	InventoryOutofband       ConditionKind = "inventoryOutofband"
	GenerateFirmwareSet      ConditionKind = "generateFirmwareSet"
)

// ConditionKinds returns the list of ConditionKinds defined.
func ConditionKinds() []ConditionKind {
	return []ConditionKind{
		FirmwareInstallOutofband,
		InventoryOutofband,
		GenerateFirmwareSet,
	}
}

// ConditionKindValid validates the ConditionKind.
func ConditionKindValid(k ConditionKind) bool {
	return slices.Contains(ConditionKinds(), k)
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
func ConditionStateValid(s ConditionState) bool {
	return slices.Contains(ConditionStates(), s)
}

// ConditionStateFinalized returns true when the state is finalized.
func ConditionStateFinalized(s ConditionState) bool {
	return slices.Contains([]ConditionState{Failed, Succeeded}, s)
}

// Parameters is an interface for Condition Parameter types
type Parameters interface {
	Validate() error
}

// Condition defines model for Condition.
type Condition struct {
	// Kind is one of ConditionKind.
	Kind ConditionKind `json:"kind,omitempty"`

	// Parameters is typed based on the ConditionKind
	Parameters Parameters `json:"parameters,omitempty"`

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
	UpdatedAt time.Time `json:"updatedAt,omitempty"`

	// CreatedAt is when this object was created.
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type InventoryOutofbandParameters struct{}

// Validate fields
func (i *InventoryOutofbandParameters) Validate() error {
	return nil
}

type GenerateFirmwareSetParameters struct {
}

// Validate fields
func (g *GenerateFirmwareSetParameters) Validate() error {
	return nil
}
