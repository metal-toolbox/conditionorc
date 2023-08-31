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
	updOnce             sync.Once
	expectedDots        = 1 // we expect keys for KV-based status updates to be facilityCode.conditionID
	errKeyFormat        = errors.New("malformed update key")
	errConditionID      = errors.New("bad condition uuid")
	errInvalidState     = errors.New("invalid condition state")
	errStaleEvent       = errors.New("event is stale")
	staleEventThreshold = 30 * time.Minute
)

func (o *Orchestrator) startUpdateListener(ctx context.Context) {
	updOnce.Do(func() {
		var span trace.Span
		ctx, span = otel.Tracer(pkgName).Start(ctx, "startUpdateListener")
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

	o.startConditionListeners(ctx, evtChan, &wg)

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

type translatorFn func(context.Context, nats.KeyValueEntry) (*v1types.ConditionUpdateEvent, error)

// startConditionListeners does what it says on the tin; iterate across all configured conditions
// and start a KV watcher for each one. We increment the waitgroup counter for each condition we
// can handle.
func (o *Orchestrator) startConditionListeners(ctx context.Context,
	evtChan chan<- *v1types.ConditionUpdateEvent, wg *sync.WaitGroup) {

	for _, def := range o.conditionDefs {
		var tf translatorFn
		var watcher nats.KeyWatcher

		kind := def.Kind

		switch kind {
		case ptypes.FirmwareInstall:
			tf = installEventFromKV
		case ptypes.InventoryOutofband:
			tf = inventoryEventFromKV
		default:
			o.logger.WithField("condition.kind", string(kind)).Warn("unsupported condition")
			continue
		}

		wg.Add(1)

		watcher, err := status.WatchConditionStatus(ctx, kind, o.facility)
		if err != nil {
			o.logger.WithError(err).WithField("condition.kind", string(kind)).Fatal("unable to get watcher")
		}

		go func() {
			defer wg.Done()
			// remember, the condition has to be true to run the loop, so "not stop" == false
			// stops the loop.
			for stop := false; !stop; {
				select {
				case <-ctx.Done():
					o.logger.WithField("condition.kind", string(kind)).Info("stopping KV update listener")
					stop = true
				case entry := <-watcher.Updates():
					if entry == nil {
						o.logger.WithField("condition.kind", string(kind)).Debug("nil KV update")
						continue
					}

					evt, err := tf(ctx, entry)
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

// XXX: This is a temporary copy of the StatusValue from Flasher. The structs shared by
// controllers and ConditionOrc will be moved to a separate "API repo" so we can break
// the direct dependencies of the apps on each other.
type flasherStatus struct {
	UpdatedAt  time.Time       `json:"updated"`
	WorkerID   string          `json:"worker"`
	TraceID    string          `json:"traceID"`
	SpanID     string          `json:"spanID"`
	Target     string          `json:"target"`
	State      string          `json:"state"`
	Status     json.RawMessage `json:"status"`
	MsgVersion int32           `json:"msgVersion"`
}

// installEventFromKV converts the Flasher-native StatusValue (the value from the KV) to a
// ConditionOrchestrator-native type that ConditionOrc can more-easily use for its
// own purposes.
func installEventFromKV(ctx context.Context, kve nats.KeyValueEntry) (*v1types.ConditionUpdateEvent, error) {
	parsedKey, err := parseStatusKVKey(kve.Key())
	if err != nil {
		return nil, err
	}

	byt := kve.Value()
	fs := flasherStatus{}
	//nolint:govet // you and gocritic can argue about it outside.
	if err := json.Unmarshal(byt, &fs); err != nil {
		return nil, err
	}

	if !ptypes.ConditionStateIsValid(ptypes.ConditionState(fs.State)) {
		return nil, errInvalidState
	}

	// skip processing any records where we're in a final state and it's older than
	// our threshold
	if ptypes.ConditionStateIsComplete(ptypes.ConditionState(fs.State)) &&
		time.Since(fs.UpdatedAt) > staleEventThreshold {
		return nil, errStaleEvent
	}

	// extract traceID and spanID
	traceID, _ := trace.TraceIDFromHex(fs.TraceID)
	spanID, _ := trace.SpanIDFromHex(fs.SpanID)

	// add a trace span
	if traceID.IsValid() && spanID.IsValid() {
		remoteSpan := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		})

		_, span := otel.Tracer(pkgName).Start(
			trace.ContextWithRemoteSpanContext(ctx, remoteSpan),
			"installEventFromKV",
		)
		defer span.End()
	}

	// validate the contents
	serverID, err := uuid.Parse(fs.Target)
	if err != nil {
		return nil, errors.Wrap(err, "parsing target id")
	}

	convState := ptypes.ConditionState(fs.State)
	if !ptypes.ConditionStateIsValid(convState) {
		return nil, errors.Wrap(errors.New("invalid condition state"), fs.State)
	}

	updEvent := &v1types.ConditionUpdateEvent{
		ConditionUpdate: v1types.ConditionUpdate{
			ConditionID: parsedKey.conditionID,
			ServerID:    serverID,
			State:       convState,
			Status:      fs.Status,
		},
		Kind: ptypes.FirmwareInstall,
	}

	return updEvent, nil
}

// XXX: need to sketch in the alloy status structure

// inventoryEventFromKV converts a raw KeyValueEntry from NATS (in the inventory
// bucket) to a ConditionUpdateEvent for publishing back to ServerService
func inventoryEventFromKV(_ context.Context, _ nats.KeyValueEntry) (*v1types.ConditionUpdateEvent, error) {
	return nil, errors.New("unimplemented")
}
