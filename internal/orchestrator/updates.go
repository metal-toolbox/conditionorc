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
	updOnce        sync.Once
	expectedDots   = 1 // we expect keys for KV-based status updates to be facilityCode.conditionID
	errKeyFormat   = errors.New("malformed update key")
	errConditionID = errors.New("bad condition uuid")
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
		status.ConnectToKVStores(o.streamBroker, o.logger, opts...)
		go o.statusKVListener(ctx)
	})
}

func (o *Orchestrator) statusKVListener(ctx context.Context) {
	// start the watchers and return the associated channels
	installWatcher, err := status.WatchFirmwareInstallStatus(ctx)
	if err != nil {
		o.logger.WithError(err).Fatal("unable to watch install status KV")
	}
	// XXX: Alloy OOB support inventoryWatcher := status.WatchInventoryStatus(ctx)
	o.logger.Info("listening for KV updates")
	for {
		var evt *v1types.ConditionUpdateEvent
		var err error
		select {
		case <-ctx.Done():
			o.logger.Info("stopping KV update listener")
			return
			// retrieve and process events sent by controllers.
		case entry := <-installWatcher.Updates():
			if entry == nil {
				o.logger.Debug("nil kv entry")
				continue
			}
			evt, err = installEventFromKV(ctx, entry)
			if err != nil {
				o.logger.WithError(err).Warn("error creating install condition event")
			}

			/* XXX: Uncomment this for out-of-band inventory support
			case entry := <-inventoryWatcher.Updates():
				evt, err := inventoryEventFromKV(entry)
				if err != nil {
					o.logger.WithError(err).Warn("error creating inventory condition event")
				} */
		}
		if evt != nil {
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
