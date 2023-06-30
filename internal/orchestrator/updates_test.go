package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/status"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/nats-io/nats-server/v2/server"
	srvtest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"

	ftypes "github.com/metal-toolbox/flasher/types"
)

func init() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
}

func startJetStreamServer(t *testing.T) *server.Server {
	t.Helper()
	opts := srvtest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	return srvtest.RunServer(&opts)
}

func jetStreamContext(t *testing.T, s *server.Server) (*nats.Conn, nats.JetStreamContext) {
	t.Helper()
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect => %v", err)
	}
	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		t.Fatalf("JetStream => %v", err)
	}
	return nc, js
}

func shutdownJetStream(t *testing.T, s *server.Server) {
	t.Helper()
	var sd string
	if config := s.JetStreamConfig(); config != nil {
		sd = config.StoreDir
	}
	s.Shutdown()
	if sd != "" {
		if err := os.RemoveAll(sd); err != nil {
			t.Fatalf("Unable to remove storage %q: %v", sd, err)
		}
	}
	s.WaitForShutdown()
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

func TestInstallEventFromKV(t *testing.T) {
	srv := startJetStreamServer(t)
	defer shutdownJetStream(t, srv)
	nc, _ := jetStreamContext(t, srv) // nc is closed on evJS.Close(), js needs no cleanup
	evJS := events.NewJetstreamFromConn(nc)
	defer evJS.Close()

	log := logrus.New()
	status.ConnectToKVStores(evJS, log, kv.WithDescription("test install event KV"))
	writeHandle, err := events.AsNatsJetStreamContext(evJS).KeyValue(string(ptypes.FirmwareInstall))
	require.NoError(t, err, "write handle")

	watcher, err := status.WatchFirmwareInstallStatus(context.TODO())
	require.NoError(t, err, "watcher")
	defer watcher.Stop()

	// add some KVs
	sv1 := ftypes.StatusValue{
		Target: uuid.New().String(),
		State:  "pending",
		Status: json.RawMessage(`{"msg":"some-status"}`),
	}
	condID := uuid.New()
	k1 := fmt.Sprintf("fc13.%s", condID)

	_, err = writeHandle.Put(k1, sv1.MustBytes())
	require.NoError(t, err)

	toCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	var entry nats.KeyValueEntry
	var stop bool
	var count int
	for !stop {
		select {
		case entry = <-watcher.Updates():
			count += 1
			if entry != nil {
				stop = true
			}
		case <-toCtx.Done():
			t.Fatal("timeout fetching from KV")
		}
	}
	t.Logf("got %d values from the KV", count)
	cancel()

	upd1, err := installEventFromKV(context.Background(), entry)
	require.NoError(t, err)
	require.Equal(t, condID, upd1.ConditionUpdate.ConditionID)
	require.Equal(t, ptypes.Pending, upd1.ConditionUpdate.State)
}
