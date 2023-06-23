package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/status"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
)

var (
	updOnce             sync.Once
	fetchEventsInterval = 1 * time.Second
	expectedDots        = 1 // we expect keys for KV-based status updates to be facilityCode.conditionID
	errKeyFormat        = errors.New("malformed update key")
	errConditionID      = errors.New("bad condition uuid")
)

func (o *Orchestrator) startUpdateListener(ctx context.Context) {
	updOnce.Do(func() {
		o.logger.Info("one-time update configuration")
		if err := ctx.Err(); err != nil {
			o.logger.WithError(err).Info("bypassing update listener start on context error")
			return
		}
		if o.statusKV {
			// XXX: this is a little split up, conditionally setting the replicas here while
			// we set the TTL in the status module. This should ge refactored after MVP.
			opts := []kv.Option{}
			if o.replicaCount > 1 {
				opts = append(opts, kv.WithReplicas(o.replicaCount))
			}
			status.ConnectToKVStores(o.streamBroker, o.logger, opts...)
			go o.statusKVListener(ctx)
		} else {
			go o.updateListener(ctx)
		}
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
		select {
		case <-ctx.Done():
			o.logger.Info("stopping KV update listener")
			return
			// retrieve and process events sent by controllers.
		case entry := <-installWatcher.Updates():
			if o.concurrencyLimit() {
				continue
			}
			if entry == nil {
				o.logger.Debug("nil kv entry")
				continue
			}
			evt, err := installEventFromKV(entry)
			if err == nil {
				_ = o.eventHandler.UpdateCondition(ctx, evt)
			} else {
				o.logger.WithError(err).Warn("error creating install condition event")
			}

			/* XXX: Uncomment this for out-of-band inventory support
			case entry := <-inventoryWatcher.Updates():
				if o.concurrencyLimit() {
					continue
				}
				evt, err := inventoryEventFromKV(entry)
				if err == nil {
				o.eventHandler.UpdateCondition(ctx, evt)
			} else {
				o.logger.WithError(err).Warn("error creating inventory condition event")
			} */
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
	Target     string          `json:"target"`
	State      string          `json:"state"`
	Status     json.RawMessage `json:"status"`
	MsgVersion int32           `json:"msgVersion"`
}

// installEventFromKV converts the Flasher-native StatusValue (the value from the KV) to a
// ConditionOrchestrator-native type that ConditionOrc can more-easily use for its
// own purposes.
func installEventFromKV(kve nats.KeyValueEntry) (*v1types.ConditionUpdateEvent, error) {
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

func (o *Orchestrator) updateListener(ctx context.Context) {
	o.logger.Info("listening for updates")
	tickerFetchEvents := time.NewTicker(fetchEventsInterval).C
	for {
		select {
		case <-ctx.Done():
			o.logger.Info("stopping update listener")
			return
		// retrieve and process events sent by controllers.
		case <-tickerFetchEvents:
			if o.concurrencyLimit() {
				continue
			}
			o.pullEvents(ctx)
		}
	}
}

func (o *Orchestrator) concurrencyLimit() bool {
	return int(o.dispatched) >= o.concurrency
}

func (o *Orchestrator) pullEvents(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		o.logger.WithError(err).Info("exit on context error")
		return
	}
	// XXX: consider having a separate context for message retrieval
	msgs, err := o.streamBroker.PullMsg(ctx, 1)
	if err != nil {
		o.logger.WithFields(
			logrus.Fields{"err": err.Error()},
		).Trace("error fetching work")
	}

	for _, msg := range msgs {
		// spawn msg process handler
		o.syncWG.Add(1)

		go func(msg events.Message) {
			defer o.syncWG.Done()

			atomic.AddInt32(&o.dispatched, 1)
			defer atomic.AddInt32(&o.dispatched, -1)

			o.processEvent(ctx, msg)
		}(msg)
	}
}
