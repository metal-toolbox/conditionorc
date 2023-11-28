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
	"github.com/nats-io/nats-server/v2/server"
	srvtest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
	"go.hollow.sh/toolbox/events/registry"
	"go.uber.org/goleak"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	rkv "github.com/metal-toolbox/rivets/kv"
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
	}

	require.False(t, o.eventNeedsReconciliation(evt))

	evt.UpdatedAt = time.Now().Add(-90 * time.Minute)
	evt.ConditionUpdate.State = rctypes.Failed

	require.True(t, o.eventNeedsReconciliation(evt))

	evt.ConditionUpdate.State = rctypes.Active
	evt.ControllerID = registry.GetID("needs-reconciliation-test")

	require.True(t, o.eventNeedsReconciliation(evt))

	evt.ControllerID = liveWorker
	require.False(t, o.eventNeedsReconciliation(evt))
}

func TestEventUpdateFromKV(t *testing.T) {
	writeHandle, err := status.GetConditionKV(rctypes.FirmwareInstall)
	require.NoError(t, err, "write handle")

	cID := registry.GetID("test-app")

	// add some KVs
	sv1 := rkv.StatusValue{
		Target:   uuid.New().String(),
		State:    "pending",
		Status:   json.RawMessage(`{"msg":"some-status"}`),
		WorkerID: cID.String(),
	}
	bogus := rkv.StatusValue{
		Target: uuid.New().String(),
		State:  "bogus",
		Status: json.RawMessage(`{"msg":"some-status"}`),
	}
	noCID := rkv.StatusValue{
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

	upd1, err := eventUpdateFromKV(context.Background(), entry, rctypes.FirmwareInstall)
	require.NoError(t, err)
	require.Equal(t, condID, upd1.ConditionUpdate.ConditionID)
	require.Equal(t, rctypes.Pending, upd1.ConditionUpdate.State)

	// bogus state should error
	entry, err = writeHandle.Get(k2)
	require.NoError(t, err)
	_, err = eventUpdateFromKV(context.Background(), entry, rctypes.FirmwareInstall)
	require.ErrorIs(t, errInvalidState, err)

	// no controller id event should error as well
	entry, err = writeHandle.Get(k3)
	require.NoError(t, err)
	_, err = eventUpdateFromKV(context.Background(), entry, rctypes.FirmwareInstall)
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
	var wg sync.WaitGroup
	testChan := make(chan *v1types.ConditionUpdateEvent)
	ctx, cancel := context.WithCancel(context.TODO())

	o.startConditionWatchers(ctx, testChan, &wg)
	o.startReconciler(ctx, &wg)

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
