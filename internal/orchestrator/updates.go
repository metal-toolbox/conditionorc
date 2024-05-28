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
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events/pkg/kv"
	"go.hollow.sh/toolbox/events/registry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	rcontroller "github.com/metal-toolbox/rivets/events/controller"
)

var (
	updOnce            sync.Once
	expectedDots       = 1 // we expect keys for KV-based status updates to be facilityCode.conditionID
	errKeyFormat       = errors.New("malformed update key")
	errConditionID     = errors.New("bad condition uuid")
	errInvalidState    = errors.New("invalid condition state")
	errCompleteEvent   = errors.New("unable to complete event")
	failedByReconciler = []byte(`{ "msg": "controller failed to process this condition in time" }`)
	reconcilerCadence  = 1 * time.Minute
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
					}).Debug("KV update")

					evt, err := parseEventUpdateFromKV(ctx, entry, kind)
					if err != nil {
						o.logger.WithError(err).WithField("rctypes.kind", string(kind)).
							Warn("error transforming status data")

						metrics.NatsKVUpdateEvent.With(
							prometheus.Labels{
								"conditionKind": string(evt.Kind),
								"valid":         "false",
							},
						).Inc()

						continue
					}

					metrics.NatsKVUpdateEvent.With(
						prometheus.Labels{
							"conditionKind": string(evt.Kind),
							"valid":         "true",
						},
					).Inc()

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
func parseEventUpdateFromKV(ctx context.Context, kve nats.KeyValueEntry,
	kind rctypes.Kind,
) (*v1types.ConditionUpdateEvent, error) {
	parsedKey, err := parseStatusKVKey(kve.Key())
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

		_, span := otel.Tracer(pkgName).Start(
			trace.ContextWithRemoteSpanContext(ctx, remoteSpan),
			"eventUpdateFromKV",
		)
		defer span.End()
	}

	updEvent := &v1types.ConditionUpdateEvent{
		ConditionUpdate: v1types.ConditionUpdate{
			ConditionID: parsedKey.conditionID,
			ServerID:    serverID,
			State:       convState,
			Status:      cs.Status,
			UpdatedAt:   cs.UpdatedAt,
		},
		Kind:         kind,
		ControllerID: controllerID,
	}

	return updEvent, nil
}

func (o *Orchestrator) getEventsToReconcile(ctx context.Context) []*v1types.ConditionUpdateEvent {
	// collect all events across multiple condition definitions
	evts := []*v1types.ConditionUpdateEvent{}
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

func (o *Orchestrator) eventUpdate(ctx context.Context, evt *v1types.ConditionUpdateEvent) error {
	if err := o.eventHandler.UpdateCondition(ctx, evt); err != nil {
		return errors.Wrap(err, "updating condition")
	}

	// nothing else to do if the condition is not finalized
	if !rctypes.StateIsComplete(evt.ConditionUpdate.State) {
		return nil
	}

	// deal with the completed event
	delErr := status.DeleteCondition(evt.Kind, o.facility, evt.ConditionUpdate.ConditionID.String())
	if delErr != nil {
		// if we fail to delete this event from the KV, the reconciler will catch it later
		// and walk this code, so return early.
		o.logger.WithError(delErr).WithFields(logrus.Fields{
			"condition.id":   evt.ConditionUpdate.ConditionID,
			"server.id":      evt.ConditionUpdate.ServerID,
			"condition.kind": evt.Kind,
		}).Warn("removing completed condition data")

		metrics.DependencyError("nats", "remove completed condition condition")

		return errors.Wrap(errCompleteEvent, delErr.Error())
	}

	metrics.ConditionCompleted.With(
		prometheus.Labels{
			"conditionKind": string(evt.Kind),
			"state":         string(evt.ConditionUpdate.State),
		},
	).Inc()

	// queue any follow-on work as required
	return o.queueFollowingCondition(ctx, evt)
}

// Queue up follow on conditions
func (o *Orchestrator) queueFollowingCondition(ctx context.Context, evt *v1types.ConditionUpdateEvent) error {
	active, err := o.repository.GetActiveCondition(ctx, evt.ConditionUpdate.ServerID)
	if err != nil && errors.Is(err, store.ErrConditionNotFound) {
		// nothing more to do
		return nil
	}

	if err != nil {
		o.logger.WithError(err).WithFields(logrus.Fields{
			"condition.id": evt.ConditionUpdate.ConditionID,
			"server.id":    evt.ConditionUpdate.ServerID,
		}).Warn("retrieving next active condition")

		metrics.DependencyError("nats", "retrieve active condition")

		return errors.Wrap(errCompleteEvent, err.Error())
	}

	// Publish the next event if that event is in the pending state.
	//
	// Seeing as we only *just* completed this event it's hard to believe we'd
	// lose the race to publish the next one, but it's possible I suppose.
	if active != nil && active.State == rctypes.Pending {
		byt := active.MustBytes()
		subject := fmt.Sprintf("%s.servers.%s", o.facility, active.Kind)
		err := o.streamBroker.Publish(ctx, subject, byt)
		if err != nil {
			o.logger.WithError(err).WithFields(logrus.Fields{
				"condition.id":   active.ID,
				"server.id":      evt.ConditionUpdate.ServerID,
				"condition.kind": active.Kind,
			}).Warn("publishing next active condition")

			metrics.DependencyError("nats", "publish-condition")

			return errors.Wrap(errCompleteEvent, err.Error())
		}

		metrics.ConditionQueued.With(
			prometheus.Labels{"conditionKind": string(active.Kind)},
		).Inc()

		o.logger.WithFields(logrus.Fields{
			"condition.id":   active.ID,
			"server.id":      evt.ConditionUpdate.ServerID,
			"condition.kind": active.Kind,
		}).Debug("published next condition in chain")
	}

	return nil
}

// This reconciles conditions in the Condition status KV - $condition.$facility
func (o *Orchestrator) eventNeedsReconciliation(evt *v1types.ConditionUpdateEvent) bool {
	// the last update should be later than the condition stale threshold
	if time.Since(evt.ConditionUpdate.UpdatedAt) < rctypes.StaleThreshold {
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
	if time.Since(lastTime) > rcontroller.LivenessStaleThreshold {
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
				// reconcile status KV entries
				evts := o.getEventsToReconcile(ctx)
				for _, evt := range evts {
					le := o.logger.WithFields(logrus.Fields{
						"conditionID":    evt.ConditionUpdate.ConditionID.String(),
						"conditionState": string(evt.ConditionUpdate.State),
						"kind":           string(evt.Kind),
					})

					if err := o.eventUpdate(ctx, evt); err != nil {
						le.WithError(err).Warn("reconciler event update")
						continue
					}

					if err := o.notifier.Send(evt); err != nil {
						le.WithError(err).Warn("reconciler event notification")
					}
				}
			}
		}
	}()
}
