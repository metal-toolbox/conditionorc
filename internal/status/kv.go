package status

import (
	"context"
	"fmt"
	"sync"
	"time"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"

	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/pkg/kv"
)

var (
	statusOnce   sync.Once
	statusTTL    = 10 * 24 * time.Hour // expire status information in 10 days
	kvCollection = map[string]nats.KeyValue{}
	errNotReady  = errors.New("condition kvs not initialized")
	errNoKV      = errors.New("no kv for condition")
	kvReady      bool
)

// ConnectToKVStores initializes all status KVs in preparation for monitoring status updates
// Any errors here are fatal, as we are failing to initialize something we are explicitly
// configured for.
func ConnectToKVStores(s events.Stream, log *logrus.Logger,
	defs ptypes.ConditionDefinitions, opts ...kv.Option) {
	js, ok := s.(*events.NatsJetstream)
	if !ok {
		log.Fatal("status via KV updates is only supported on NATS")
	}

	statusOpts := []kv.Option{
		kv.WithTTL(statusTTL),
	}

	if len(opts) > 0 {
		statusOpts = append(statusOpts, opts...)
	}

	statusOnce.Do(func() {
		for _, def := range defs {
			kind := string(def.Kind)
			hdl, err := kv.CreateOrBindKVBucket(js, kind, statusOpts...)
			if err != nil {
				log.WithError(err).
					WithField("kv.type", kind).
					Fatal("unable to initialize NATS KV for status")
			}
			kvCollection[kind] = hdl
		}
		kvReady = true
	})
}

// WatchConditionStatus specializes some generic NATS functionality, mainly to keep
// the callers cleaner of the NATS-specific details.
func WatchConditionStatus(ctx context.Context, kind ptypes.ConditionKind, facility string) (nats.KeyWatcher, error) {
	if !kvReady {
		return nil, errNotReady
	}

	kv, ok := kvCollection[string(kind)]
	if !ok {
		return nil, errors.Wrap(errNoKV, string(kind))
	}

	// format the facility as a NATS subject to use as a filter for relevant KVs
	keyStr := fmt.Sprintf("%s.*", facility)
	return kv.Watch(keyStr, nats.Context(ctx))
}

// GetConditionKV returns the raw NATS KeyValue interface for the bucket associated
// with the given condition type.
func GetConditionKV(kind ptypes.ConditionKind) (nats.KeyValue, error) {
	if !kvReady {
		return nil, errNotReady
	}

	kv, ok := kvCollection[string(kind)]
	if !ok {
		return nil, errors.Wrap(errNoKV, string(kind))
	}

	return kv, nil
}
