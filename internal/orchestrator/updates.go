package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/status"
	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/conditions/types"
	rctypes "github.com/metal-toolbox/rivets/v2/condition"
	"github.com/metal-toolbox/rivets/v2/events/pkg/kv"
	"github.com/metal-toolbox/rivets/v2/events/registry"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var (
	updOnce            sync.Once
	expectedDots       = 1 // we expect keys for KV-based status updates to be facilityCode.conditionID
	errKeyFormat       = errors.New("malformed update key")
	errConditionID     = errors.New("bad condition uuid")
	errInvalidState    = errors.New("invalid condition state")
	errCompleteEvent   = errors.New("unable to complete event")
	failedByReconciler = []byte(`{ "msg": "controller failed to process this condition in time" }`)
	reconcilerCadence  = 10 * time.Minute
	errRetryThis       = errors.New("retry this operation")
)

func (o *Orchestrator) startUpdateMonitor(ctx context.Context) {
	updOnce.Do(func() {
		var span trace.Span
		ctx, span = otel.Tracer(pkgName).Start(ctx, "startUpdateMonitor")
		defer span.End()

		o.logger.Info("one-time update configuration")
		if err := ctx.Err(); err != nil {
			o.logger.WithError(err).Info("bypassing update listener start on context error")
			return
		}
		// XXX: this is a little split up, conditionally setting the replicas here while
		// we set the TTL in the status module. This should ge refactored after MVP.
		opts := []kv.Option{}
		if o.replicaCount > 1 {
			opts = append(opts, kv.WithReplicas(o.replicaCount))
		}
		status.ConnectToKVStores(o.streamBroker, o.logger, o.conditionDefs, opts...)
		go o.kvStatusPublisher(ctx)
	})
}

// kvStatusPublisher creates a channel for ConditionUpdateEvents, starts the watchers
// for its configured conditions, then polls the ConditionUpdateEvent channel and publishes
// any results.
func (o *Orchestrator) kvStatusPublisher(ctx context.Context) {
	var wg sync.WaitGroup

	evtChan := make(chan *v1types.ConditionUpdateEvent)

	o.startConditionWatchers(ctx, evtChan, &wg)

	o.logger.Debug("waiting for KV updates")

	for stop := false; !stop; {
		select {
		case <-ctx.Done():
			o.logger.Debug("stopping KV update listener")
			stop = true

		// retrieve and process events sent by controllers KV updates.
		case evt := <-evtChan:
			le := o.logger.WithFields(logrus.Fields{
				"conditionID":    evt.ConditionUpdate.ConditionID.String(),
				"conditionState": string(evt.ConditionUpdate.State),
				"kind":           string(evt.Kind),
			})

			if err := o.eventUpdate(ctx, evt); err != nil {
				le.WithError(err).Warn("performing event update")
				continue
			}

			if err := o.notifier.Send(evt); err != nil {
				le.WithError(err).Warn("sending notification")
				// notifications are advisory, so if we fail to notify we keep processing
			}
		}
	}

	wg.Wait()
	close(evtChan)

	o.logger.Debug("shut down KV updates")
}

// startConditionWatchers does what it says on the tin; iterate across all configured conditions
// and start a KV watcher for each one. We increment the waitgroup counter for each condition we
// can handle.
func (o *Orchestrator) startConditionWatchers(ctx context.Context,
	evtChan chan<- *v1types.ConditionUpdateEvent, wg *sync.WaitGroup,
) {
	for _, def := range o.conditionDefs {
		var watcher nats.KeyWatcher

		kind := def.Kind

		wg.Add(1)

		watcher, err := status.WatchConditionStatus(ctx, kind, o.facility)
		if err != nil {
			o.logger.WithError(err).WithField("rctypes.kind", string(kind)).Fatal("unable to get watcher")
		}

		go func() {
			defer wg.Done()
			// NATS will send an nil if the connection is live but there are no updates. We expect one per use
			// of the update channel, sent before it transitions to a blocking behavior.
			sawNil := false
			for keepRunning := true; keepRunning; {
				select {
				case <-ctx.Done():
					o.logger.WithField("rctypes.kind", string(kind)).Info("stopping KV update listener")
					keepRunning = false
					//nolint:errcheck,gocritic
					watcher.Stop()
				case entry := <-watcher.Updates():
					if entry == nil {
						if sawNil {
							o.logger.WithField("rctypes.kind", string(kind)).Info("refreshing KV watcher")
							//nolint:errcheck,gocritic
							watcher.Stop()
							watcher, err = status.WatchConditionStatus(ctx, kind, o.facility)
							if err != nil {
								// if NATS is unavailable, stopping is best
								o.logger.WithError(err).
									WithField("rctypes.kind", string(kind)).Fatal("unable to refresh KV watcher")
							}
							sawNil = false
							continue
						}
						o.logger.WithField("rctypes.kind", string(kind)).Debug("nil KV update")
						sawNil = true
						continue
					}

					o.logger.WithFields(logrus.Fields{
						"rctypes.kind": string(kind),
						"entry.key":    entry.Key(),
					}).Trace("KV update")

					evt, err := parseEventUpdateFromKV(ctx, entry, kind)
					if err != nil {
						o.logger.WithError(err).WithField("rctypes.kind", string(kind)).
							Warn("error transforming status data")

						continue
					}

					evtChan <- evt
				}
			}
		}()
	}
}

type statusKey struct {
	facility    string
	conditionID uuid.UUID
}

// We expect keys in the format of facilityCode.uuid-as-string. If that expectation
// is not met, it's an error and we drop the update.
func parseStatusKVKey(key string) (*statusKey, error) {
	if expectedDots != strings.Count(key, ".") {
		return nil, errKeyFormat
	}
	elements := strings.Split(key, ".")

	conditionID, err := uuid.Parse(elements[1])
	if err != nil {
		return nil, errConditionID
	}

	return &statusKey{
		facility:    elements[0],
		conditionID: conditionID,
	}, nil
}

// parseEventUpdateFromKV converts the stored rivets.StatusValue (the value from the KV) to a
// ConditionOrchestrator-native type that ConditionOrc can more-easily use for its
// own purposes.
func parseEventUpdateFromKV(ctx context.Context, kve nats.KeyValueEntry, kind rctypes.Kind) (updEvent *v1types.ConditionUpdateEvent, err error) {
	var parsedKey *statusKey

	// deferred method collects telemetry on failed kve parse errors
	defer func() {
		if err == nil {
			return
		}

		var conditionID, serverID string
		if parsedKey != nil {
			conditionID = parsedKey.conditionID.String()
		}

		if updEvent != nil {
			serverID = updEvent.ServerID.String()
		}

		metrics.RegisterSpanEventKVParseError(
			trace.SpanFromContext(ctx),
			kve.Key(),
			serverID,
			conditionID,
			string(kind),
			err.Error(),
		)
	}()

	parsedKey, err = parseStatusKVKey(kve.Key())
	if err != nil {
		return nil, err
	}

	byt := kve.Value()
	cs := rctypes.StatusValue{}
	//nolint:govet // you and gocritic can argue about it outside.
	if err := json.Unmarshal(byt, &cs); err != nil {
		return nil, err
	}

	// validate the contents
	serverID, err := uuid.Parse(cs.Target)
	if err != nil {
		return nil, errors.Wrap(err, "parsing target id")
	}

	convState := rctypes.State(cs.State)
	if !rctypes.StateIsValid(convState) {
		return nil, errInvalidState
	}

	controllerID, err := registry.ControllerIDFromString(cs.WorkerID)
	if err != nil {
		return nil, errors.Wrap(err, "parsing worker id")
	}

	// extract traceID and spanID
	traceID, _ := trace.TraceIDFromHex(cs.TraceID)
	spanID, _ := trace.SpanIDFromHex(cs.SpanID)

	// add a trace span
	if traceID.IsValid() && spanID.IsValid() {
		remoteSpan := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		})

		var span trace.Span
		ctx, span = otel.Tracer(pkgName).Start(
			trace.ContextWithRemoteSpanContext(ctx, remoteSpan),
			"parseEventUpdateFromKV",
		)
		defer span.End()
	}

	updEvent = &v1types.ConditionUpdateEvent{
		ConditionUpdate: v1types.ConditionUpdate{
			ConditionID: parsedKey.conditionID,
			ServerID:    serverID,
			State:       convState,
			Status:      cs.Status,
			UpdatedAt:   cs.UpdatedAt,
			CreatedAt:   cs.CreatedAt,
		},
		Kind:         kind,
		ControllerID: controllerID,
	}

	return updEvent, nil
}

func (o *Orchestrator) getEventsToReconcile(ctx context.Context) (evts []*v1types.ConditionUpdateEvent) {
	// collect all events across multiple condition definitions
	for _, def := range o.conditionDefs {
		kind := def.Kind
		entries, err := status.GetAllConditions(kind, o.facility)
		if err != nil {
			o.logger.WithError(err).WithField("rctypes.kind", string(kind)).
				Warn("reconciler error in condition status lookup")
			continue
		}

		for _, kve := range entries {
			evt, err := parseEventUpdateFromKV(ctx, kve, kind)
			if err != nil {
				o.logger.WithError(err).WithFields(logrus.Fields{
					"rctypes.kind": string(kind),
					"kv.key":       kve.Key(),
				}).Warn("reconciler skipping malformed update")
				continue
			}

			if o.eventNeedsReconciliation(evt) {
				if !rctypes.StateIsComplete(evt.ConditionUpdate.State) {
					failedStatus := failedByReconciler

					metrics.ConditionReconcileStale.With(
						prometheus.Labels{"conditionKind": string(evt.Kind)},
					).Inc()

					// append to existing status record when its present
					statusRecord, err := rctypes.StatusRecordFromMessage(evt.Status)
					if err != nil {
						o.logger.WithError(err).WithFields(logrus.Fields{
							"condition.id": evt.ConditionID.String(),
							"rctypes.kind": string(kind),
							"kv.key":       kve.Key(),
						}).Warn("error parsing existing status record from condition")
					} else {
						statusRecord.Append(string(failedStatus))
					}

					// we need to deal with this event, so mark it failed
					evt.ConditionUpdate.State = rctypes.Failed
					evt.ConditionUpdate.Status = failedStatus
				}

				evts = append(evts, evt)
			}
		}
	}

	return evts
}

// XXX: testing note -- all the functions called here (ConditionUpdateEvent::Validate(),
// Repository::GetActiveCondition(), and ConditionUpdateEvent::MergeExisting()) are all tested in their
// respective modules, so I'm skipping testing this function.
func (o *Orchestrator) mergeUpdate(ctx context.Context, updEvt *v1types.ConditionUpdateEvent) (*rctypes.Condition, error) {
	_, span := otel.Tracer(pkgName).Start(ctx, "orchestrator.mergeUpdate")
	defer span.End()

	metrics.RegisterSpanEvent(
		span,
		updEvt.ServerID.String(),
		updEvt.ConditionID.String(),
		string(updEvt.Kind),
		"mergeUpdate",
	)

	if err := updEvt.Validate(); err != nil {
		o.logger.WithError(err).WithFields(logrus.Fields{
			"server_id":      updEvt.ConditionUpdate.ServerID,
			"condition_kind": updEvt.Kind,
		}).Error("conditionUpdateEvent validate error")
		return nil, err
	}

	// query existing condition
	existing, err := o.repository.GetActiveCondition(ctx, updEvt.ConditionUpdate.ServerID)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			o.logger.WithFields(logrus.Fields{
				"server_id":      updEvt.ConditionUpdate.ServerID,
				"condition_id":   updEvt.ConditionUpdate.ConditionID,
				"condition_kind": updEvt.Kind,
			}).Error("no existing pending/active condition found for update")
			return nil, errors.Wrap(err, "fetching active condition to update")
		}
		return nil, errors.Wrap(errRetryThis, err.Error())
	}

	// merge update with existing
	revisedCondition, err := updEvt.MergeExisting(existing)
	if err != nil {
		o.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":       updEvt.ConditionUpdate.ServerID,
			"conditionKind":  updEvt.Kind,
			"incoming_state": updEvt.State,
			"existing_state": existing.State,
		}).Warn("condition merge failed")
		return nil, errors.Wrap(err, "merging condition update")
	}

	return revisedCondition, nil
}

func (o *Orchestrator) eventUpdate(ctx context.Context, evt *v1types.ConditionUpdateEvent) error {
	updatedCondition, err := o.mergeUpdate(ctx, evt)
	if err != nil {
		return errors.Wrap(err, "updating condition")
	}

	// commit the update
	if err := o.repository.Update(ctx, updatedCondition.Target, updatedCondition); err != nil {
		o.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":    updatedCondition.Target.String(),
			"conditionID": updatedCondition.ID.String(),
		}).Info("condition update failed")
		if errors.Is(err, store.ErrConditionComplete) {
			// we *should* not be here because the mergeUpdate function should have failed before
			return err
		}
		return errors.Wrap(errRetryThis, err.Error())
	}

	// nothing else to do if the condition is not finalized
	if !rctypes.StateIsComplete(updatedCondition.State) {
		return nil
	}
	return o.finalizeCondition(ctx, updatedCondition)
}

func (o *Orchestrator) finalizeCondition(ctx context.Context, cond *rctypes.Condition) error {
	// if we fail to update the event history or to delete this event from the KV,
	// the reconciler  will catch it later and walk this code, so return early. It
	// is kosher to replay event history iff the contents of that history (id,
	// condition kind, target, parameters, state and status) are identical.
	if err := o.db.WriteEventHistory(ctx, cond); err != nil {
		o.logger.WithError(err).WithFields(logrus.Fields{
			"condition.id":   cond.ID.String(),
			"server.id":      cond.Target.String(),
			"condition.kind": cond.Kind,
		}).Warn("updating event history")

		metrics.DependencyError("fleetdb", "update event history")

		return errors.Wrap(errCompleteEvent, err.Error())
	}

	delErr := status.DeleteCondition(cond.Kind, o.facility, cond.ID.String())
	if delErr != nil {
		o.logger.WithError(delErr).WithFields(logrus.Fields{
			"condition.id":   cond.ID.String(),
			"server.id":      cond.Target.String(),
			"condition.kind": cond.Kind,
		}).Warn("removing completed condition data")

		metrics.DependencyError("nats", "remove completed condition condition")

		return errors.Wrap(errCompleteEvent, delErr.Error())
	}

	metrics.ConditionCompleted.With(
		prometheus.Labels{
			"conditionKind": string(cond.Kind),
			"state":         string(cond.State),
		},
	).Inc()

	// queue any follow-on work as required
	return o.queueFollowingCondition(ctx, cond)
}

// Queue up follow on conditions
func (o *Orchestrator) queueFollowingCondition(ctx context.Context, cond *rctypes.Condition) error {
	active, err := o.repository.GetActiveCondition(ctx, cond.Target)
	if err != nil && errors.Is(err, store.ErrConditionNotFound) {
		// nothing more to do
		return nil
	}

	if err != nil {
		o.logger.WithError(err).WithFields(logrus.Fields{
			"condition.id": cond.ID.String(),
			"server.id":    cond.Target.String(),
		}).Warn("retrieving next active condition")

		metrics.DependencyError("nats", "retrieve active condition")

		return fmt.Errorf("%w:retrieving active condition:%w", errCompleteEvent, err)
	}
	// Conditions for controllers that run inband are not published to the JS,
	// they are retrieved by the inband controllers themselves through the Orchestrator API.
	if !active.StreamPublishRequired() {
		o.logger.WithFields(logrus.Fields{
			"condition.id":   active.ID,
			"server.id":      active.Target.String(),
			"condition.kind": active.Kind,
		}).Debug("not publishing inband condition")
		return nil
	}

	// If we're here we have to publish the next event, if that event is in the pending state
	if active != nil && active.State == rctypes.Pending {
		byt := active.MustBytes()
		subject := fmt.Sprintf("%s.servers.%s", o.facility, active.Kind)
		err := o.streamBroker.Publish(ctx, subject, byt)
		if err != nil {
			o.logger.WithError(err).WithFields(logrus.Fields{
				"condition.id":   active.ID.String(),
				"server.id":      active.Target.String(),
				"condition.kind": active.Kind,
			}).Warn("publishing next active condition")

			metrics.DependencyError("nats", "publish-condition")
			metrics.PublishErrors.WithLabelValues(string(active.Kind)).Inc()

			return fmt.Errorf("%w:publishing next event:%w", errCompleteEvent, err)
		}

		metrics.ConditionQueued.With(
			prometheus.Labels{"conditionKind": string(active.Kind)},
		).Inc()

		o.logger.WithFields(logrus.Fields{
			"condition.id":   active.ID,
			"server.id":      active.Target.String(),
			"condition.kind": active.Kind,
		}).Debug("published next condition in chain")
	}

	return nil
}

// This reconciles conditions in the Condition status KV - $condition.$facility
func (o *Orchestrator) eventNeedsReconciliation(evt *v1types.ConditionUpdateEvent) bool {
	// the last update should be later than the condition stale threshold
	if time.Since(evt.ConditionUpdate.UpdatedAt) < rctypes.StatusStaleThreshold {
		return false
	}

	le := o.logger.WithFields(logrus.Fields{
		"conditionID":    evt.ConditionUpdate.ConditionID.String(),
		"conditionState": string(evt.ConditionUpdate.State),
		"last.update":    evt.UpdatedAt.String(),
		"kind":           string(evt.Kind),
		"controllerID":   evt.ControllerID, //
	})

	// if the event is in a final state it should be handled
	if rctypes.StateIsComplete(evt.ConditionUpdate.State) {
		le.Info("condition in final state")
		return true
	}

	// if the controller has not checked in within the liveliness TTL
	lastTime, err := registry.LastContact(evt.ControllerID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			le.Info("controller not registered")
			return true
		}

		le.WithError(err).Warn("error checking registry")
		metrics.DependencyError("nats-active-conditions", "get")
		return false
	}

	// controller most likely dead
	if time.Since(lastTime) > rctypes.LivenessStaleThreshold {
		le.WithField(
			"last.checkin",
			time.Since(lastTime),
		).Info("controller check in timed out")
		return true
	}

	return false
}

func (o *Orchestrator) startReconciler(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)

	go func() {
		defer wg.Done()

		ticker := time.NewTicker(reconcilerCadence)
		defer ticker.Stop()

		for keepRunning := true; keepRunning; {
			select {
			case <-ctx.Done():
				o.logger.Info("reconciler signaled to exit")
				keepRunning = false

			case <-ticker.C:
				o.reconcileStatusKVEntries(ctx)
			}
		}
	}()
}

func (o *Orchestrator) reconcileStatusKVEntries(ctx context.Context) {
	evts := o.getEventsToReconcile(ctx)
	for _, evt := range evts {
		le := o.logger.WithFields(logrus.Fields{
			"conditionID":    evt.ConditionUpdate.ConditionID.String(),
			"conditionState": string(evt.ConditionUpdate.State),
			"kind":           string(evt.Kind),
		})

		if err := o.eventUpdate(ctx, evt); err != nil {
			le.WithError(err).Warn("reconciler event update")
			if errors.Is(err, errRetryThis) {
				// arguably dependencies are in a weird state, maybe return?
				continue
			}
		}

		le.Info("condition reconciled")
		// don't need this anymore, get rid of it
		if err := status.DeleteCondition(evt.Kind, o.facility, evt.ConditionID.String()); err != nil {
			le.WithError(err).Warn("deleting condition on reconciliation")
		}

		if err := o.notifier.Send(evt); err != nil {
			le.WithError(err).Warn("reconciler event notification")
		}
	}
}
