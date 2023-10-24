package store

import (
	"context"
	"os"
	"testing"
	"time"

	rctypes "github.com/metal-toolbox/rivets/condition"

	"github.com/google/uuid"
	"github.com/nats-io/nats-server/v2/server"
	srvtest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
)

var (
	nc     *nats.Conn
	js     nats.JetStreamContext
	evJS   *events.NatsJetstream
	logger *logrus.Logger
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

// do some one-time setup
func TestMain(m *testing.M) {
	logger = logrus.New()

	srv := startJetStreamServer()
	defer shutdownJetStream(srv)
	nc, js = jetStreamContext(srv) // nc is closed on evJS.Close(), js needs no cleanup
	evJS = events.NewJetstreamFromConn(nc)
	defer evJS.Close()

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestCRUD(t *testing.T) {
	serverID := uuid.New()

	kind := rctypes.Kind("test-kind")

	condition := &rctypes.Condition{
		ID:    uuid.New(),
		Kind:  kind,
		State: rctypes.Pending,
	}

	logger := &logrus.Logger{}

	bucket, err := kv.CreateOrBindKVBucket(evJS, bucketName)
	require.NoError(t, err, "setup NATS kv")

	store := &natsStore{
		bucket: bucket,
		log:    logger,
	}

	// look for a condition in an empty bucket and find nothing
	_, err = store.Get(context.TODO(), serverID, kind)
	require.ErrorIs(t, err, ErrConditionNotFound)

	// add a condition
	err = store.Create(context.TODO(), serverID, condition)
	require.NoError(t, err)

	// find the new condition
	c, err := store.Get(context.TODO(), serverID, kind)
	require.NoError(t, err)
	require.Equal(t, condition.ID.String(), c.ID.String())

	// update the condition
	condition.State = rctypes.Active
	err = store.Update(context.TODO(), serverID, condition)
	require.NoError(t, err)

	// read the new condition
	c, err = store.Get(context.TODO(), serverID, kind)
	require.NoError(t, err)
	require.Equal(t, condition.State, c.State)

	// OK, get rid of it
	err = store.Delete(context.TODO(), serverID, kind)
	require.ErrorIs(t, err, ErrConditionNotComplete)

	condition.State = rctypes.Succeeded
	err = store.Update(context.TODO(), serverID, condition)
	require.NoError(t, err)

	err = store.Delete(context.TODO(), serverID, kind)
	require.NoError(t, err)

	// And now it's not here anymore
	_, err = store.Get(context.TODO(), serverID, kind)
	require.ErrorIs(t, err, ErrConditionNotFound)
}
