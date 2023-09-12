package status

import (
	"context"
	"os"
	"testing"
	"time"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"

	"github.com/nats-io/nats-server/v2/server"
	srvtest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/events"
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

type shutdownFunc func()

func startTestJetStream(t *testing.T) (*events.NatsJetstream, shutdownFunc) {
	t.Helper()
	srv := startJetStreamServer(t)
	conn, _ := jetStreamContext(t, srv)
	evJS := events.NewJetstreamFromConn(conn)
	return evJS, func() {
		evJS.Close()
		shutdownJetStream(t, srv)
	}
}

func TestStatusKV(t *testing.T) {
	t.Parallel()
	js, testDone := startTestJetStream(t)
	defer testDone()

	defs := ptypes.ConditionDefinitions{
		&ptypes.ConditionDefinition{
			Kind:      ptypes.ConditionKind("test-event"),
			Exclusive: true,
		},
	}

	// pre-ready returns an error
	require.False(t, kvReady)
	_, err := WatchConditionStatus(context.TODO(), ptypes.ConditionKind("bogus"), "my-facility")
	require.ErrorIs(t, err, errNotReady, "wrong error")

	_, err = GetConditionKV(ptypes.ConditionKind("bogus"))
	require.ErrorIs(t, err, errNotReady, "wrong error")

	ConnectToKVStores(js, &logrus.Logger{}, defs)
	require.True(t, kvReady)

	// bogus condition name returns an error
	_, err = WatchConditionStatus(context.TODO(), ptypes.ConditionKind("bogus"), "my-facility")
	require.ErrorIs(t, err, errNoKV, "wrong error")

	_, err = GetConditionKV(ptypes.ConditionKind("bogus"))
	require.ErrorIs(t, err, errNoKV, "wrong error")

	// use the configured kind and get a real object back
	_, err = WatchConditionStatus(context.TODO(), ptypes.ConditionKind("test-event"), "my-facility")
	require.NoError(t, err)

	testKind := ptypes.ConditionKind("test-event")
	handle, err := GetConditionKV(testKind)
	require.NoError(t, err)

	// write some data to the KV store to exercise our get methods
	_, err = handle.PutString("badkeyformat", "bogus data")
	require.NoError(t, err)
	_, err = handle.PutString("fc13.firstkey", "This is some fc13 data")
	require.NoError(t, err)
	_, err = handle.PutString("fc1.firstkey", "This is some fc1 data")
	require.NoError(t, err)
	_, err = handle.PutString("fc13.secondkey", "This is more fc13 data")
	require.NoError(t, err)

	c, err := GetSingleCondition(testKind, "fc1", "firstkey")
	require.NotNil(t, c)
	require.NoError(t, err)

	es, err := GetAllConditions(testKind, "fc13")
	require.NoError(t, err)
	require.Equal(t, 2, len(es))

	// Delete fc13.secondkey and it's gone
	err = DeleteCondition(testKind, "fc13", "secondkey")
	require.NoError(t, err)
	c, err = GetSingleCondition(testKind, "fc13", "secondkey")
	require.Nil(t, c)
	require.ErrorIs(t, err, nats.ErrKeyNotFound)

	es, err = GetAllConditions(testKind, "fc8")
	require.NoError(t, err)
	require.Equal(t, 0, len(es))
}
