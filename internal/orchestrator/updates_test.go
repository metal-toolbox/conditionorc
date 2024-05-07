package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/status"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/nats-io/nats-server/v2/server"
	srvtest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
	"go.hollow.sh/toolbox/events/registry"
	"go.uber.org/goleak"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	rcontroller "github.com/metal-toolbox/rivets/events/controller"
)

var (
	nc         *nats.Conn
	js         nats.JetStreamContext
	evJS       *events.NatsJetstream
	logger     *logrus.Logger
	defs       rctypes.Definitions
	liveWorker = registry.GetID("updates-test")
)

func init() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
}

func startJetStreamServer() *server.Server {
	opts := srvtest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	return srvtest.RunServer(&opts)
}

func jetStreamContext(s *server.Server) (*nats.Conn, nats.JetStreamContext) {
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		logger.Fatalf("connect => %v", err)
	}
	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		logger.Fatalf("JetStream => %v", err)
	}
	return nc, js
}

func shutdownJetStream(s *server.Server) {
	var sd string
	if config := s.JetStreamConfig(); config != nil {
		sd = config.StoreDir
	}
	s.Shutdown()
	if sd != "" {
		if err := os.RemoveAll(sd); err != nil {
			logger.Fatalf("Unable to remove storage %q: %v", sd, err)
		}
	}
	s.WaitForShutdown()
}

// let's pretend we're a conditioon-orchestrator app and do some one-time setup
func TestMain(m *testing.M) {
	logger = logrus.New()

	srv := startJetStreamServer()
	defer shutdownJetStream(srv)
	nc, js = jetStreamContext(srv) // nc is closed on evJS.Close(), js needs no cleanup
	evJS = events.NewJetstreamFromConn(nc)
	defer evJS.Close()

	defs = rctypes.Definitions{
		&rctypes.Definition{
			Kind: rctypes.FirmwareInstall,
		},
		&rctypes.Definition{
			Kind: rctypes.Inventory,
		},
		&rctypes.Definition{
			Kind: rctypes.Kind("bogus"),
		},
	}

	status.ConnectToKVStores(evJS, logger, defs, kv.WithDescription("test watchers"))
	registry.InitializeRegistryWithOptions(evJS) // we don't need a TTL or replicas
	if err := registry.RegisterController(liveWorker); err != nil {
		logger.WithError(err).Fatal("registering controller id")
	}

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestParseStatusKey(t *testing.T) {
	t.Parallel()
	goodKey := "fc13.0099138a-2645-4c27-afe6-a30b613f59ae"
	periods := "too.many.periods"
	badId := "fc13.not-a-uuuid"

	key, err := parseStatusKVKey(goodKey)
	require.NoError(t, err)
	require.Equal(t, uuid.MustParse("0099138a-2645-4c27-afe6-a30b613f59ae"), key.conditionID)

	_, err = parseStatusKVKey(periods)
	require.Error(t, err)
	require.ErrorIs(t, err, errKeyFormat)

	_, err = parseStatusKVKey(badId)
	require.Error(t, err)
	require.ErrorIs(t, err, errConditionID)
}

func TestEventNeedsReconciliation(t *testing.T) {
	t.Parallel()

	o := Orchestrator{
		logger: logger,
	}

	evt := &v1types.ConditionUpdateEvent{
		ConditionUpdate: v1types.ConditionUpdate{
			UpdatedAt: time.Now(),
		},
		ControllerID: registry.GetID("needs-reconciliation-test"),
	}
	require.False(t, o.eventNeedsReconciliation(evt), "Condition UpdatedAt")

	evt.UpdatedAt = time.Now().Add(-90 * time.Minute)
	evt.ConditionUpdate.State = rctypes.Failed
	require.True(t, o.eventNeedsReconciliation(evt), "Condition finalized")

	evt.ConditionUpdate.State = rctypes.Active
	require.True(t, o.eventNeedsReconciliation(evt), "Condition finalized")

	evt.ControllerID = liveWorker
	require.False(t, o.eventNeedsReconciliation(evt), "controller active")
}

func TestFilterToReconcile(t *testing.T) {
	t.Parallel()

	// test server, condition ID 1
	cid1 := uuid.New()
	sid1 := uuid.New()

	// test server, condition ID 2
	cid2 := uuid.New()
	sid2 := uuid.New()

	// test timestamps
	createdTS := time.Now()
	updatedTS := createdTS.Add(1 * time.Minute)

	tests := []struct {
		name         string
		records      []*store.ConditionRecord                 // records in active-conditions
		updateEvents map[string]*v1types.ConditionUpdateEvent // status updates from status KV
		wantCreates  []*rctypes.Condition                     // expected creates
		wantUpdates  []*v1types.ConditionUpdateEvent          // expected updates
	}{
		{
			name: "pending in active-conditions and pending in status KV",
			records: []*store.ConditionRecord{
				{
					ID:    cid1,
					State: rctypes.Pending,
					Conditions: []*rctypes.Condition{
						{
							ID:     cid1,
							Kind:   rctypes.FirmwareInstall,
							State:  rctypes.Pending,
							Target: sid1,
						},
						{
							ID:     cid1,
							Kind:   rctypes.Inventory,
							State:  rctypes.Pending,
							Target: sid1,
						},
					},
				},
			},
			updateEvents: map[string]*v1types.ConditionUpdateEvent{
				sid1.String(): {
					ConditionUpdate: v1types.ConditionUpdate{
						ServerID: sid1,
						State:    rctypes.Pending,
					},
				},
			},
			wantCreates: nil,
			wantUpdates: nil,
		},
		{
			name: "pending in active-conditions exceeded stale threshold and not listed in status KV",
			records: []*store.ConditionRecord{
				{
					ID:    cid1,
					State: rctypes.Pending,
					Conditions: []*rctypes.Condition{
						{
							ID:     cid1,
							Kind:   rctypes.FirmwareInstall,
							State:  rctypes.Pending,
							Target: sid1,
							// exceed thresholds
							CreatedAt: createdTS.Add(-rctypes.StaleThreshold - 2*time.Minute),
							UpdatedAt: updatedTS.Add(-rcontroller.StatusStaleThreshold - 2*time.Minute),
						},
						{
							ID:        cid1,
							Kind:      rctypes.Inventory,
							State:     rctypes.Pending,
							Target:    sid1,
							CreatedAt: createdTS.Add(-rctypes.StaleThreshold + 1),
							UpdatedAt: time.Time{},
						},
					},
				},
			},
			updateEvents: map[string]*v1types.ConditionUpdateEvent{},
			wantCreates:  nil,
			wantUpdates: []*v1types.ConditionUpdateEvent{
				{
					ConditionUpdate: v1types.ConditionUpdate{
						ConditionID: cid1,
						ServerID:    sid1,
						State:       rctypes.Failed,
						Status:      failedByReconciler,
					},
					Kind: rctypes.FirmwareInstall,
				},
			},
		},
		{
			name: "pending in active-conditions within stale threshold and not listed in status KV",
			records: []*store.ConditionRecord{
				{
					ID:    cid1,
					State: rctypes.Pending,
					Conditions: []*rctypes.Condition{
						{
							ID:        cid1,
							Kind:      rctypes.FirmwareInstall,
							State:     rctypes.Pending,
							Target:    sid1,
							CreatedAt: createdTS,
							UpdatedAt: updatedTS,
						},
						{
							ID:        cid1,
							Kind:      rctypes.Inventory,
							State:     rctypes.Pending,
							Target:    sid1,
							CreatedAt: createdTS,
							UpdatedAt: updatedTS,
						},
					},
				},
			},
			updateEvents: map[string]*v1types.ConditionUpdateEvent{},
			wantCreates:  nil,
			wantUpdates:  nil,
		},
		{
			name:    "not listed in active-conditions and in-complete in status KV",
			records: []*store.ConditionRecord{},
			updateEvents: map[string]*v1types.ConditionUpdateEvent{
				sid1.String(): {
					ConditionUpdate: v1types.ConditionUpdate{
						ConditionID: cid1,
						ServerID:    sid1,
						State:       rctypes.Active,
						Status:      []byte(`{"msg": "running"}`),
						UpdatedAt:   updatedTS,
						CreatedAt:   createdTS,
					},
				},
			},
			wantCreates: []*rctypes.Condition{
				{
					Version:   rctypes.ConditionStructVersion,
					ID:        cid1,
					Target:    sid1,
					State:     rctypes.Active,
					Status:    []byte(`{"msg": "running"}`),
					UpdatedAt: updatedTS,
					CreatedAt: createdTS,
				},
			},
			wantUpdates: nil,
		},
		{
			name: "multiple updates and creates",
			records: []*store.ConditionRecord{
				// record to be updated
				{
					ID:    cid1,
					State: rctypes.Active,
					Conditions: []*rctypes.Condition{
						{
							ID:     cid1,
							Kind:   rctypes.FirmwareInstall,
							State:  rctypes.Pending,
							Target: sid1,
							// thresholds on record exceeded
							CreatedAt: createdTS.Add(-rctypes.StaleThreshold - 2*time.Minute),
							UpdatedAt: updatedTS.Add(-rcontroller.StatusStaleThreshold - 2*time.Minute),
						},
					},
				},
			},
			updateEvents: map[string]*v1types.ConditionUpdateEvent{
				// expect create for event
				sid2.String(): {
					ConditionUpdate: v1types.ConditionUpdate{
						ConditionID: cid2,
						ServerID:    sid2,
						State:       rctypes.Active,
						Status:      []byte(`{"msg": "running"}`),
						UpdatedAt:   updatedTS,
						CreatedAt:   createdTS,
					},
				},
			},
			wantCreates: []*rctypes.Condition{
				{
					Version:   rctypes.ConditionStructVersion,
					ID:        cid2,
					Target:    sid2,
					State:     rctypes.Active,
					Status:    []byte(`{"msg": "running"}`),
					UpdatedAt: updatedTS,
					CreatedAt: createdTS,
				},
			},
			wantUpdates: []*v1types.ConditionUpdateEvent{
				{
					ConditionUpdate: v1types.ConditionUpdate{
						ConditionID: cid1,
						ServerID:    sid1,
						State:       rctypes.Failed,
						Status:      failedByReconciler,
					},
					Kind: rctypes.FirmwareInstall,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCreates, gotUpdates := filterToReconcile(tc.records, tc.updateEvents)
			if len(tc.wantCreates) == 0 {
				assert.Len(t, gotCreates, 0)
			} else {
				assert.Equal(t, tc.wantCreates, gotCreates)
			}

			if len(tc.wantUpdates) == 0 {
				assert.Len(t, gotUpdates, 0)
			} else {
				assert.Len(t, gotUpdates, 1)
				assert.WithinDuration(t, time.Now(), gotUpdates[0].UpdatedAt, 3*time.Second)
				gotUpdates[0].UpdatedAt = time.Time{}
				assert.Equal(t, gotUpdates, tc.wantUpdates)
			}
		})
	}
}

func TestEventUpdateFromKV(t *testing.T) {
	writeHandle, err := status.GetConditionKV(rctypes.FirmwareInstall)
	require.NoError(t, err, "write handle")

	cID := registry.GetID("test-app")

	// add some KVs
	sv1 := rctypes.StatusValue{
		Target:   uuid.New().String(),
		State:    "pending",
		Status:   json.RawMessage(`{"msg":"some-status"}`),
		WorkerID: cID.String(),
	}
	bogus := rctypes.StatusValue{
		Target: uuid.New().String(),
		State:  "bogus",
		Status: json.RawMessage(`{"msg":"some-status"}`),
	}
	noCID := rctypes.StatusValue{
		Target:    uuid.New().String(),
		State:     "failed",
		Status:    json.RawMessage(`{"msg":"some-status"}`),
		UpdatedAt: time.Now().Add(-90 * time.Minute),
	}

	condID := uuid.New()
	k1 := fmt.Sprintf("fc13.%s", condID)
	k2 := fmt.Sprintf("fc13.%s", uuid.New())
	k3 := fmt.Sprintf("fc13.%s", uuid.New())

	_, err = writeHandle.Put(k1, sv1.MustBytes())
	require.NoError(t, err)

	_, err = writeHandle.Put(k2, bogus.MustBytes())
	require.NoError(t, err)

	_, err = writeHandle.Put(k3, noCID.MustBytes())
	require.NoError(t, err)

	// test the expected good KV entry
	entry, err := writeHandle.Get(k1)
	require.NoError(t, err)

	upd1, err := parseEventUpdateFromKV(context.Background(), entry, rctypes.FirmwareInstall)
	require.NoError(t, err)
	require.Equal(t, condID, upd1.ConditionUpdate.ConditionID)
	require.Equal(t, rctypes.Pending, upd1.ConditionUpdate.State)

	// bogus state should error
	entry, err = writeHandle.Get(k2)
	require.NoError(t, err)
	_, err = parseEventUpdateFromKV(context.Background(), entry, rctypes.FirmwareInstall)
	require.ErrorIs(t, errInvalidState, err)

	// no controller id event should error as well
	entry, err = writeHandle.Get(k3)
	require.NoError(t, err)
	_, err = parseEventUpdateFromKV(context.Background(), entry, rctypes.FirmwareInstall)
	require.ErrorIs(t, err, registry.ErrBadFormat)
}

func TestConditionListenersExit(t *testing.T) {
	ic := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ic)

	o := Orchestrator{
		logger:        logger,
		streamBroker:  evJS,
		facility:      "test",
		conditionDefs: defs,
	}

	// here we're acting in place of kvStatusPublisher to make sure that we can orchestrate the watchers
	testChan := make(chan *v1types.ConditionUpdateEvent)
	ctx, cancel := context.WithCancel(context.TODO())

	wg := &sync.WaitGroup{}
	o.startConditionWatchers(ctx, testChan, wg)
	o.startReconciler(ctx, wg)

	sentinelChan := make(chan struct{})
	toCtx, toCancel := context.WithTimeout(context.TODO(), time.Second)
	defer toCancel()

	var testPassed bool

	go func() {
		wg.Wait()
		testPassed = true
		close(sentinelChan)
	}()

	// cancel the original context and we should unwind all our listeners
	cancel()

	select {
	case <-toCtx.Done():
		t.Log("watchers did not exit")
	case <-sentinelChan:
	}
	require.True(t, testPassed)
}
