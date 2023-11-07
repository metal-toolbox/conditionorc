package store

/* This file implements the store interface backed by a NATS KV bucket.
   This bucket uses the server UUID as a key and uses the below-defined
   conditionRecord as its data.
*/

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var (
	bucketName = "active-conditions"
	bucketOpts = []kv.Option{
		kv.WithDescription("tracking active conditions on servers"),
		kv.WithTTL(10 * 24 * time.Hour), // XXX: we could keep more history here, but might need more storage
	}
)

type natsStore struct {
	log    *logrus.Logger
	bucket nats.KeyValue
}

type conditionRecord struct {
	ID         uuid.UUID            `json:"id"`
	State      rctypes.State        `json:"state"`
	Conditions []*rctypes.Condition `json:"conditions"`
}

func (c conditionRecord) MustJSON() json.RawMessage {
	byt, err := json.Marshal(&c)
	if err != nil {
		panic("bad condition record serialize")
	}
	return byt
}

func (c *conditionRecord) FromJSON(rm json.RawMessage) error {
	return json.Unmarshal(rm, c)
}

func natsError(op string) {
	metrics.DependencyError("nats-active-conditions", op)
}

func newNatsRepository(log *logrus.Logger, stream events.Stream, replicaCount int) (*natsStore, error) {
	evJS, ok := stream.(*events.NatsJetstream)
	if !ok {
		// play stupid games, win stupid prizes
		panic("bad stream implementation")
	}

	kvOpts := bucketOpts
	if replicaCount > 1 {
		kvOpts = append(kvOpts, kv.WithReplicas(replicaCount))
	}

	kvHandle, err := kv.CreateOrBindKVBucket(evJS, bucketName, kvOpts...)
	if err != nil {
		log.WithError(err).Debug("binding kv bucket")
		return nil, errors.Wrap(err, "binding active conditions bucket")
	}
	return &natsStore{
		log:    log,
		bucket: kvHandle,
	}, nil
}

// we must find an active condition with a kind that matches in order to successfully return
// a condition here.
func (n *natsStore) findConditionRecord(srvID uuid.UUID, kind rctypes.Kind) (*conditionRecord,
	*rctypes.Condition, error) {
	le := n.log.WithFields(logrus.Fields{
		"serverID":      srvID.String(),
		"conditionKind": kind,
	})

	kve, err := n.bucket.Get(srvID.String())
	switch {
	case err == nil:
	case errors.Is(nats.ErrKeyNotFound, err):
		le.Debug("no active condition for device")
		return nil, nil, ErrConditionNotFound
	default:
		natsError("get")
		le.WithError(err).Warn("looking up active condition")
		return nil, nil, errors.Wrap(ErrRepository, err.Error())
	}

	var cr conditionRecord
	if err := cr.FromJSON(kve.Value()); err != nil {
		le.WithError(err).Warn("bad condition data")
		return nil, nil, errors.Wrap(ErrRepository, err.Error())
	}

	var found *rctypes.Condition
	for _, c := range cr.Conditions {
		if c.Kind == kind {
			// XXX: this assumes that there is only a single example of a given condition
			// kind in a record. That is reasonable for current use-cases.
			found = c
			break
		}
	}

	if found == nil {
		le.WithField("conditionID", cr.ID.String()).Warn("condition record missing specified type")
		return nil, nil, ErrConditionNotFound
	}

	return &cr, found, nil
}

// Get returns the active condition of the given Kind on the server.
func (n *natsStore) Get(ctx context.Context, serverID uuid.UUID, kind rctypes.Kind) (*rctypes.Condition, error) {
	_, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.Get")
	defer span.End()

	_, condition, err := n.findConditionRecord(serverID, kind)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if span.IsRecording() {
		span.SetAttributes(attribute.Stringer("conditionID", condition.ID))
	}

	return condition, nil
}

// GetActiveCondition returns any condition for the given server-id that is not in a final state.
// This is a locking mechanism to ensure that conditions (or sets of condtions) are only executed
// one-at-a-time. That is, even though "Pending" condition isn't "Active" per se, it is still
// just waiting on a controller and it's equivalent for the purposes of checking if another condition
// can be started.
func (n *natsStore) GetActiveCondition(ctx context.Context, srvID uuid.UUID) (*rctypes.Condition, error) {
	_, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.GetActiveCondition")
	defer span.End()

	le := n.log.WithFields(logrus.Fields{
		"serverID": srvID.String(),
	})

	kve, err := n.bucket.Get(srvID.String())
	switch {
	case err == nil:
	case errors.Is(nats.ErrKeyNotFound, err):
		le.Debug("no active condition for device")
		return nil, nil
	default:
		natsError("get")
		le.WithError(err).Warn("looking up active condition")
		return nil, errors.Wrap(ErrRepository, err.Error())
	}

	var cr conditionRecord
	if err := cr.FromJSON(kve.Value()); err != nil {
		le.WithError(err).Warn("bad condition data")
		return nil, errors.Wrap(ErrRepository, err.Error())
	}

	if rctypes.StateIsComplete(cr.State) {
		return nil, nil
	}

	var active *rctypes.Condition
	for _, cond := range cr.Conditions {
		if !rctypes.StateIsComplete(cond.State) {
			le.WithFields(logrus.Fields{
				"condition.id":    cond.ID,
				"condition.state": string(cond.State),
			}).Debug("found active condition for server")
			active = cond
			break
		}
	}

	return active, nil
}

// Create a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition payload and to check any existing condition before creating.
func (n *natsStore) Create(ctx context.Context, serverID uuid.UUID, condition *rctypes.Condition) error {
	_, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.Create")
	defer span.End()

	le := n.log.WithFields(logrus.Fields{
		"serverID":      serverID.String(),
		"conditionKind": condition.Kind,
		"conditionID":   condition.ID.String(),
	})

	cr := conditionRecord{
		ID:    condition.ID,
		State: rctypes.Pending,
		Conditions: []*rctypes.Condition{
			condition,
		},
	}

	// XXX: We *should* be using Create() below, but we can't while we're in transition between Serverservice
	// and NATS as repository implementations. Once we have a better story for how we handle any existing
	// conditionRecord, we can revisit the decision to use the NATS Put API
	// _, err := n.bucket.Create(serverID.String(), cr.MustJSON())
	_, err := n.bucket.Put(serverID.String(), cr.MustJSON())
	if err != nil {
		natsError("create")
		span.RecordError(err)
		le.WithError(err).Warn("writing condition to storage")
	}
	return err
}

// Update a condition on a server.
// @id: required
// @condition: required
//
// Note: it's up to the caller to validate the condition update payload. The condition to update must
// exist, and it must be in a non-final state. The existing condition will be replaced by the incoming
// parameter. If applicable, the state of the conditionRecord will be updated as well.
func (n *natsStore) Update(ctx context.Context, serverID uuid.UUID, condition *rctypes.Condition) error {
	_, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.Update")
	defer span.End()

	le := n.log.WithFields(logrus.Fields{
		"serverID":      serverID.String(),
		"conditionKind": condition.Kind,
		"conditionID":   condition.ID.String(),
	})

	cr, orig, err := n.findConditionRecord(serverID, condition.Kind)
	if err != nil {
		span.RecordError(err)
		le.WithError(err).Warn("condition lookup failure on update")
		return err
	}

	// stupid games, stupid prizes sanity check
	if cr.ID != condition.ID {
		le.WithField("record.ID", cr.ID.String()).Warn("condition id mismatch")
		return errors.Wrap(ErrRepository, "condition id mismatch")
	}

	cr.State = condition.State

	for idx, cond := range cr.Conditions {
		i := idx
		c := cond
		if c == orig {
			cr.Conditions[i] = condition
			break
		}
	}

	_, err = n.bucket.Put(serverID.String(), cr.MustJSON())
	if err != nil {
		natsError("update")
		span.RecordError(err)
		le.WithError(err).Warn("writing condition record on update")
		return err
	}

	return nil
}

// Delete removes a condition from being associated with a server. It adheres to the notion that
// deletes are idempotent; requesting the deletion of a non-existent condition is not an error on
// the part of this component. This enables repetition of a failed step that composes some
// operations that succeeded and some that need to be retried.
func (n *natsStore) Delete(ctx context.Context, serverID uuid.UUID, kind rctypes.Kind) error {
	_, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.Delete")
	defer span.End()

	le := n.log.WithFields(logrus.Fields{
		"serverID":      serverID.String(),
		"conditionKind": kind,
	})

	cr, _, err := n.findConditionRecord(serverID, kind)

	if errors.Is(err, ErrConditionNotFound) {
		le.Info("request to delete non-existent condition")
		return nil
	}

	if err != nil {
		span.RecordError(err)
		le.WithError(err).Warn("condition lookup failure on delete")
		return err
	}

	if !rctypes.StateIsComplete(cr.State) {
		return ErrConditionNotComplete
	}

	err = n.bucket.Delete(serverID.String())
	if err != nil {
		natsError("delete")
		span.RecordError(err)
		le.WithError(err).Warn("deleting condition record")
	}
	return err
}
