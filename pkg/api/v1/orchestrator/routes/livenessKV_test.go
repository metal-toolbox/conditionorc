package routes

import (
	"testing"
	"time"

	"github.com/metal-toolbox/rivets/events"
	"github.com/metal-toolbox/rivets/events/registry"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	srvtest "github.com/nats-io/nats-server/v2/test"
)

func runNATSServer(t *testing.T) *server.Server {
	opts := srvtest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()

	return srvtest.RunServer(&opts)
}

func shutdownNATSServer(t *testing.T, s *server.Server) {
	t.Helper()
	s.Shutdown()
	s.WaitForShutdown()
}
func TestLiveness(t *testing.T) {
	// Start a NATS server
	ns := runNATSServer(t)
	defer shutdownNATSServer(t, ns)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	// Nats conn as nats JS
	njs := events.NewJetstreamFromConn(nc)
	defer njs.Close()

	// Initialize registry
	err = registry.InitializeRegistryWithOptions(njs)
	require.NoError(t, err)

	// init liveness
	logger := logrus.New()
	liveness, err := initLivenessKV(logger, njs)
	require.NoError(t, err)
	controllerID := "test-controller"

	t.Run("Register", func(t *testing.T) {
		id, err := liveness.register(controllerID)
		require.NoError(t, err)
		assert.NotNil(t, id)

		// Verify the controller is registered using LastContact
		lastContact, err := registry.LastContact(id)
		require.NoError(t, err)
		assert.True(t, lastContact.After(time.Now().Add(-1*time.Second)))
	})

	t.Run("Successful checkin", func(t *testing.T) {
		id, err := liveness.register(controllerID)
		require.NoError(t, err)

		initialContact, err := registry.LastContact(id)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		err = liveness.checkin(id)
		require.NoError(t, err)

		newContact, err := registry.LastContact(id)
		require.NoError(t, err)
		assert.True(t, newContact.After(initialContact))
	})
}
