package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/status"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"go.hollow.sh/toolbox/events/pkg/kv"
)

var (
	updOnce         sync.Once
	expectedDots    = 1 // we expect keys for KV-based status updates to be facilityCode.conditionID
	errKeyFormat    = errors.New("malformed update key")
	errConditionID  = errors.New("bad condition uuid")
	errInvalidState = errors.New("invalid condition state")
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

		// retrieve and process events sent by controllers.
		case evt := <-evtChan:
			le := o.logger.WithFields(logrus.Fields{
				"conditionID":    evt.ConditionUpdate.ConditionID.String(),
				"conditionState": string(evt.ConditionUpdate.State),
				"kind":           string(evt.Kind),
			})

			if err := o.eventHandler.UpdateCondition(ctx, evt); err != nil {
				le.WithError(err).Warn("updating condition")
			}

			if err := o.notifier.Send(evt); err != nil {
				le.WithError(err).Warn("sending notification")
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
	evtChan chan<- *v1types.ConditionUpdateEvent, wg *sync.WaitGroup) {

	for _, def := range o.conditionDefs {
		var watcher nats.KeyWatcher

		kind := def.Kind

		wg.Add(1)

		watcher, err := status.WatchConditionStatus(ctx, kind, o.facility)
		if err != nil {
			o.logger.WithError(err).WithField("condition.kind", string(kind)).Fatal("unable to get watcher")
		}

		go func() {
			defer wg.Done()
			for keepRunning := true; keepRunning; {
				select {
				case <-ctx.Done():
					o.logger.WithField("condition.kind", string(kind)).Info("stopping KV update listener")
					keepRunning = false
				case entry := <-watcher.Updates():
					if entry == nil {
						o.logger.WithField("condition.kind", string(kind)).Debug("nil KV update")
						continue
					}

					o.logger.WithFields(logrus.Fields{
						"condition.kind": string(kind),
						"entry.key":      entry.Key(),
					}).Debug("KV update")

					evt, err := eventUpdateFromKV(ctx, entry, kind)
					if err != nil {
						o.logger.WithError(err).WithField("condition.kind", string(kind)).
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

// XXX: This is a temporary subset of the StatusValue from Flasher and the rivets repo.
// it needs to live here until both Flasher and Alloy have integrated with rivets and
// deployed.
type controllerStatus struct {
	UpdatedAt time.Time       `json:"updated"`
	TraceID   string          `json:"traceID"`
	SpanID    string          `json:"spanID"`
	Target    string          `json:"target"`
	State     string          `json:"state"`
	Status    json.RawMessage `json:"status"`
}

// eventUpdateFromKV converts the stored rivets.StatusValue (the value from the KV) to a
// ConditionOrchestrator-native type that ConditionOrc can more-easily use for its
// own purposes.
func eventUpdateFromKV(ctx context.Context, kve nats.KeyValueEntry,
	kind ptypes.ConditionKind) (*v1types.ConditionUpdateEvent, error) {
	parsedKey, err := parseStatusKVKey(kve.Key())
	if err != nil {
		return nil, err
	}

	byt := kve.Value()
	cs := controllerStatus{}
	//nolint:govet // you and gocritic can argue about it outside.
	if err := json.Unmarshal(byt, &cs); err != nil {
		return nil, err
	}

	// validate the contents
	serverID, err := uuid.Parse(cs.Target)
	if err != nil {
		return nil, errors.Wrap(err, "parsing target id")
	}

	convState := ptypes.ConditionState(cs.State)
	if !ptypes.ConditionStateIsValid(convState) {
		return nil, errInvalidState
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
		},
		Kind: kind,
	}

	return updEvent, nil
}
