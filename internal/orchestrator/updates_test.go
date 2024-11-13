// nolint
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/status"
	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/conditions/types"
	rctypes "github.com/metal-toolbox/rivets/v2/condition"
	"github.com/metal-toolbox/rivets/v2/events"
	"github.com/metal-toolbox/rivets/v2/events/pkg/kv"
	"github.com/metal-toolbox/rivets/v2/events/registry"
	"github.com/nats-io/nats-server/v2/server"
	srvtest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
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

func startJetStreamServer(t *testing.T) *server.Server {
	t.Helper()

	opts := srvtest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()

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

func shutdownJetStream(t *testing.T, s *server.Server) {
	t.Helper()
	s.Shutdown()
	s.WaitForShutdown()
}

// let's pretend we're a condition-orchestrator app and do some one-time setup
func TestMain(m *testing.M) {
	logger = logrus.New()

	t := &testing.T{}
	srv := startJetStreamServer(t)
	defer shutdownJetStream(t, srv)
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

func newCleanStatusKV(t *testing.T, kind rctypes.Kind) nats.KeyValue {
	t.Helper()

	sKV, err := status.GetConditionKV(kind)
	if err != nil {
		t.Fatal(err)
	}

	kvDeleteAll(t, sKV)
	return sKV
}

func newCleanActiveConditionsKV(t *testing.T) nats.KeyValue {
	t.Helper()

	// active-conditions KV handle
	acKV, err := kv.CreateOrBindKVBucket(evJS, store.ActiveConditionBucket)
	if err != nil {
		t.Fatal(err)
	}

	kvDeleteAll(t, acKV)
	return acKV
}

func kvDeleteAll(t *testing.T, kv nats.KeyValue) {
	t.Helper()

	// clean up all keys for this test
	purge, err := kv.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		t.Fatal(err)
	}

	for _, k := range purge {
		if err := kv.Delete(k); err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
			t.Fatal(err)
		}
	}
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

	evt.UpdatedAt = time.Now().Add(-1 * rctypes.StaleThreshold)
	evt.ConditionUpdate.State = rctypes.Failed
	require.True(t, o.eventNeedsReconciliation(evt), "Condition finalized")

	evt.ConditionUpdate.State = rctypes.Active
	require.True(t, o.eventNeedsReconciliation(evt), "Condition finalized")

	evt.ControllerID = liveWorker
	require.False(t, o.eventNeedsReconciliation(evt), "controller active")
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

func TestQueueFollowingCondition(t *testing.T) {
	serverID := uuid.New()
	t.Run("no following work", func(t *testing.T) {
		condID := uuid.New()
		repo := store.NewMockRepository(t)
		repo.On("GetActiveCondition", mock.Anything, serverID).Return(nil, store.ErrConditionNotFound).Once()
		o := &Orchestrator{
			logger:     logger,
			repository: repo,
		}
		condArg := &rctypes.Condition{ID: condID, Target: serverID}
		require.NoError(t, o.queueFollowingCondition(context.TODO(), condArg))
	})
	t.Run("lookup error", func(t *testing.T) {
		condID := uuid.New()
		repo := store.NewMockRepository(t)
		repo.On("GetActiveCondition", mock.Anything, serverID).Return(nil, errors.New("pound sand")).Once()
		o := &Orchestrator{
			logger:     logger,
			repository: repo,
		}
		condArg := &rctypes.Condition{ID: condID, Target: serverID}
		err := o.queueFollowingCondition(context.TODO(), condArg)
		require.Error(t, err)
		require.ErrorIs(t, err, errCompleteEvent)
	})
	t.Run("publishing error", func(t *testing.T) {
		condID := uuid.New()
		repo := store.NewMockRepository(t)
		next := &rctypes.Condition{
			ID:     condID,
			Kind:   rctypes.Kind("following-kind"),
			Target: serverID,
			State:  rctypes.Pending,
		}
		condArg := &rctypes.Condition{ID: condID, Target: serverID}
		repo.On("GetActiveCondition", mock.Anything, serverID).Return(next, nil).Once()
		stream := events.NewMockStream(t)
		subject := "fc-13.servers.following-kind"
		stream.On("Publish", mock.Anything, subject, mock.Anything).Return(errors.New("pound sand")).Once()
		o := &Orchestrator{
			facility:     "fc-13",
			logger:       logger,
			repository:   repo,
			streamBroker: stream,
		}
		err := o.queueFollowingCondition(context.TODO(), condArg)
		require.Error(t, err)
		require.ErrorIs(t, err, errCompleteEvent)
	})
	t.Run("next condition state is not pending", func(t *testing.T) {
		condID := uuid.New()
		repo := store.NewMockRepository(t)
		next := &rctypes.Condition{
			ID:     condID,
			Kind:   rctypes.Kind("following-kind"),
			Target: serverID,
			State:  rctypes.Active,
		}
		condArg := &rctypes.Condition{ID: condID, Target: serverID}
		repo.On("GetActiveCondition", mock.Anything, serverID).Return(next, nil).Once()
		o := &Orchestrator{
			logger:     logger,
			repository: repo,
		}
		err := o.queueFollowingCondition(context.TODO(), condArg)
		require.NoError(t, err)
	})
	t.Run("successful publish", func(t *testing.T) {
		condID := uuid.New()
		repo := store.NewMockRepository(t)
		next := &rctypes.Condition{
			ID:     condID,
			Kind:   rctypes.Kind("following-kind"),
			Target: serverID,
			State:  rctypes.Pending,
		}
		condArg := &rctypes.Condition{ID: condID, Target: serverID}
		repo.On("GetActiveCondition", mock.Anything, serverID).Return(next, nil).Once()
		stream := events.NewMockStream(t)
		subject := "fc-13.servers.following-kind"
		stream.On("Publish", mock.Anything, subject, mock.Anything).Return(nil).Once()
		o := &Orchestrator{
			facility:     "fc-13",
			logger:       logger,
			repository:   repo,
			streamBroker: stream,
		}
		err := o.queueFollowingCondition(context.TODO(), condArg)
		require.NoError(t, err)
	})
}
