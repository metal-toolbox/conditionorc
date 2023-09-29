package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/pkg/errors"

	"go.hollow.sh/toolbox/events/registry"
)

var (
	errBadUpdateTarget         error = errors.New("no existing condition found for update")
	errResourceVersionMismatch error = errors.New("resource version mismatch, retry request with current resourceVersion")
	errInvalidStateTransition  error = errors.New("invalid state transition")

	errUpdatePayload error = errors.New("invalid payload for update")
)

type ServerResponse struct {
	StatusCode int                 `json:"statusCode,omitempty"`
	Message    string              `json:"message,omitempty"`
	Records    *ConditionsResponse `json:"records,omitempty"`
}

// ConditionsResponse is the response returned for listing multiple conditions on a server.
type ConditionsResponse struct {
	ServerID   uuid.UUID            `json:"serverID,omitempty"`
	Conditions []*rctypes.Condition `json:"conditions,omitempty"`
}

// ConditionCreate is the request payload to create a condition with its parameters on server.
type ConditionCreate struct {
	Exclusive  bool            `json:"exclusive"`
	Parameters json.RawMessage `json:"parameters"`
	Fault      *rctypes.Fault  `json:"fault,omitempty"`
}

// NewCondition returns a new Condition type.
func (c *ConditionCreate) NewCondition(kind rctypes.Kind) *rctypes.Condition {
	return &rctypes.Condition{
		ID:         uuid.New(),
		Version:    rctypes.ConditionStructVersion,
		Kind:       kind,
		State:      rctypes.Pending,
		Exclusive:  c.Exclusive,
		Parameters: c.Parameters,
		Fault:      c.Fault,
	}
}

// ConditionUpdate is the request payload to update an existing rctypes.
type ConditionUpdate struct {
	ConditionID     uuid.UUID       `json:"conditionID"`
	ServerID        uuid.UUID       `json:"serverID"`
	State           rctypes.State   `json:"state,omitempty"`
	Status          json.RawMessage `json:"status,omitempty"`
	ResourceVersion int64           `json:"resourceVersion"`
}

func (c *ConditionUpdate) Validate() error {
	if c.ConditionID == uuid.Nil {
		return errors.Wrap(errUpdatePayload, "ConditionID not set")
	}

	if c.ServerID == uuid.Nil {
		return errors.Wrap(errUpdatePayload, "ServerID not set")
	}

	if c.ResourceVersion == 0 {
		return errors.Wrap(errUpdatePayload, "ResourceVersion not set")
	}

	if c.State == "" || c.Status == nil {
		return errors.Wrap(errUpdatePayload, "state and status attributes are expected")
	}

	return nil
}

// ConditionUpdateEvent is the payload received for a condition update over the event stream.
type ConditionUpdateEvent struct {
	ConditionUpdate
	Kind                  rctypes.Kind `json:"kind"`
	UpdatedAt             time.Time    `json:"updatedAt"`
	registry.ControllerID `json:"controllerID"`
}

// Validate checks for required attributes.
//
// Note:
// The ResourceVersion attribute is not validated for updates through events,
// this is because the controllers do not perform requests for the existing condition
// since implementing a Request-Reply pattern on the NATS Jetstream is tideous and not recommended.
//
// The NATS Jetstream guarantees ordered delivery, as long as there is a single consumer of the event,
// for now we're deploying a single orchestrator instance in each facility and so this check is not required.
// In the case that we require multiple orchestrators in a facility the stream would need to be partitioned
// to ensure ordered delivery.
//
// ref:
// https://github.com/nats-io/nats.py/discussions/221
// https://github.com/nats-io/nats.go/discussions/970#discussioncomment-2690789
//
// TODO: move this note into the messaging architecture doc.
func (c *ConditionUpdateEvent) Validate() error {
	if c.Kind == "" {
		return errors.Wrap(errUpdatePayload, "Kind attribute expected")
	}

	if c.State == "" || c.Status == nil {
		return errors.Wrap(errUpdatePayload, "state and status attributes are expected")
	}

	return nil
}

// MergeExisting when given an existing condition, validates the update based on existing values
// and returns a condition that can be passed to the repository for update.
//
// The resourceVersion is not updated here and is left for the repository Store to update.
//
// This method makes sure that update does not overwrite existing data inadvertently.
func (c *ConditionUpdate) MergeExisting(existing *rctypes.Condition, compareResourceVersion bool) (*rctypes.Condition, error) {
	// 1. condition must already exist for update.
	if existing == nil {
		return nil, errBadUpdateTarget
	}

	if existing.ID != c.ConditionID {
		// condition identifier must match
		return nil, errBadUpdateTarget
	}

	if compareResourceVersion && existing.ResourceVersion != c.ResourceVersion {
		// resourceVersion must match
		return nil, errResourceVersionMismatch
	}

	// transition is valid
	if !existing.State.TransitionValid(c.State) {
		return nil, errInvalidStateTransition
	}

	return &rctypes.Condition{
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
