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
	ftypes "github.com/metal-toolbox/flasher/types"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
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
		if err := ctx.Err(); err != nil {
			o.logger.WithError(err).Info("bypassing update listener start on context error")
			return
		}
		if o.statusKV {
			status.ConnectToKVStores(o.streamBroker, o.logger)
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
		case kv := <-installWatcher.Updates():
			if o.concurrencyLimit() {
				continue
			}
			if kv == nil {
				o.logger.Debug("nil kv entry")
				continue
			}
			evt, err := installEventFromKV(kv)
			if err == nil {
				o.eventHandler.UpdateCondition(ctx, evt)
			} else {
				o.logger.WithError(err).Warn("error creating install condition event")
			}

			/* XXX: Uncomment this for out-of-band inventory support
			case kv := <-inventoryWatcher.Updates():
				if o.concurrencyLimit() {
					continue
				}
				evt, err := inventoryEventFromKV(kv)
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

// installEventFromKV converts the Flasher-native StatusValue (the value from the KV) to a
// ConditionOrchestrator-native type that ConditionOrc can more-easily use for its
// own purposes.
func installEventFromKV(kv nats.KeyValueEntry) (*v1types.ConditionUpdateEvent, error) {
	parsedKey, err := parseStatusKVKey(kv.Key())
	if err != nil {
		return nil, err
	}

	byt := kv.Value()
	sv := ftypes.StatusValue{}
	if err := json.Unmarshal(byt, &sv); err != nil {
		return nil, err
	}

	// validate the contents
	serverID, err := uuid.Parse(sv.Target)
	if err != nil {
		return nil, errors.Wrap(err, "parsing target id")
	}

	convState := ptypes.ConditionState(sv.State)
	if !ptypes.ConditionStateIsValid(convState) {
		return nil, errors.Wrap(errors.New("invalid condition state"), sv.State)
	}

	updEvent := &v1types.ConditionUpdateEvent{
		ConditionUpdate: v1types.ConditionUpdate{
			ConditionID: parsedKey.conditionID,
			ServerID:    serverID,
			State:       convState,
			Status:      sv.Status,
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
