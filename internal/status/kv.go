package status

import (
	"context"
	"time"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"

	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
)

var (
	statusTTL         = 10 * 24 * time.Hour // expire status information in 10 days
	statusReplicas    = 3
	firmwareInstallKV nats.KeyValue
)

// ConnectToKVStores initializes all status KVs in preparation for monitoring status updates
// Any errors here are fatal, as we are failing to initialize something we are explicitly
// configured for.
func ConnectToKVStores(s events.Stream, log *logrus.Logger, opts ...kv.Option) {
	js, ok := s.(*events.NatsJetstream)
	if !ok {
		log.Fatal("status via KV updates is only supported on NATS")
	}

	defaultOpts := []kv.Option{
		kv.WithTTL(statusTTL),
		kv.WithReplicas(statusReplicas),
	}

	if len(opts) == 0 {
		opts = defaultOpts
	}

	var err error
	firmwareInstallKV, err = kv.CreateOrBindKVBucket(js, string(ptypes.FirmwareInstall), opts...)
	if err != nil {
		log.WithError(err).Fatal("unable to initialize NATS KV for firmware install")
	}

	/* inventoryKV, err := kv.CreateOrBindKV(js, string(ptypes.FirmwareInstall), opts...)
		if err != nil {
		log.WithErr(err).Fatal("unable to initialize NATS KV for inventory")
	}*/

}

// WatchFirmwareInstallStatus specializes some generic NATS functionality, mainly to keep
// the callers cleaner of the NATS-specific details.
func WatchFirmwareInstallStatus(ctx context.Context) (nats.KeyWatcher, error) {
	// we can restrict the keys we watch (e.g. by facility code) here by using
	// the KV Watch function instead.
	return firmwareInstallKV.WatchAll(nats.Context(ctx))
}
