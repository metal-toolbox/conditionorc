package store

/* This file implements the store interface backed by a NATS KV bucket.
   This bucket uses the server UUID as a key and uses the below-defined
   conditionRecord as its data.
*/

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/model"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	sservice "go.hollow.sh/serverservice/pkg/api/v1"
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
	client *sservice.Client
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

func newNatsRepository(client *sservice.Client, log *logrus.Logger, stream events.Stream, replicaCount int) (*natsStore, error) {
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
		client: client,
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

// GetServer returns the facility for the requested server id.
// XXX: this doesn't belong in this interface. We use it *only* to get a facility code.
func (n *natsStore) GetServer(ctx context.Context, serverID uuid.UUID) (*model.Server, error) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.GetServer")
	defer span.End()

	// list attributes on a server
	obj, _, err := n.client.Get(otelCtx, serverID)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrServerNotFound
		}

		n.log.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"error":    err,
			"method":   "GetServer",
		}).Warn("error reaching serverservice")

		serverServiceError("get-server")

		return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
	}

	return &model.Server{ID: obj.UUID, FacilityCode: obj.FacilityCode}, nil
}

// List retrieves any active conditions
func (n *natsStore) List(ctx context.Context, srvID uuid.UUID, incState rctypes.State) ([]*rctypes.Condition, error) {
	_, span := otel.Tracer(pkgName).Start(ctx, "NatsStore.List")
	defer span.End()
	le := n.log.WithFields(logrus.Fields{
		"serverID": srvID.String(),
	})

	// ugh, so much copy-pasta
	kve, err := n.bucket.Get(srvID.String())
	switch {
	case err == nil:
	case errors.Is(nats.ErrKeyNotFound, err):
		le.Debug("no active condition for device")
		return nil, ErrConditionNotFound
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

	var found bool
	var outgoing rctypes.Condition
	for _, c := range cr.Conditions {
		if !rctypes.StateIsComplete(c.State) {
			// The first incomplete condition is good enough
			found = true
			outgoing = *c
			// make sure to match the requested parameters to short circuit the loops in the handler.
			// we don't care what the actual state of the found condition is; the fact that it's incomplete
			// is enough to signal to the API handler that it should *not* accept a new condition
			// for this server
			outgoing.Exclusive = true
			outgoing.State = incState
			break
		}
	}

	if !found {
		return nil, ErrConditionNotFound
	}

	return []*rctypes.Condition{
		&outgoing,
	}, nil
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

	_, err := n.bucket.Create(serverID.String(), cr.MustJSON())
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
