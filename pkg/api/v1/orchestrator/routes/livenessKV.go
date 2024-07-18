package routes

import (
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/rivets/events"
	"github.com/metal-toolbox/rivets/events/registry"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	errRegistryCheckin = errors.New("error in controller check-in")
)

type livenessKV interface {
	register(controllerID string) (registry.ControllerID, error)
	checkin(id registry.ControllerID) error
}

// implements the livenessKV interface
type liveness struct {
	logger *logrus.Logger
}

func initLivenessKV(l *logrus.Logger, stream events.Stream) (livenessKV, error) {
	// set up the active-controller registry handle
	err := registerRegistryKVHandle(stream)
	if err != nil {
		return nil, errors.Wrap(err, "unable to initialize controller registry KV handle")
	}

	return &liveness{l}, nil
}

// Sets up the registry KV handle in the registry package so callers can invoke methods on the active-controllers registry
func registerRegistryKVHandle(stream events.Stream) error {
	// Note: No CreateOrBindCall is made to the bucket here,
	// since we expect that the Orchestrator or other controllers will create this bucket
	// before any http based controller shows up.
	//
	// If this trips over  nats.ErrBucketNotFound, then lets add the CreateOrBind method
	njs := stream.(*events.NatsJetstream)

	handle, err := events.AsNatsJetStreamContext(njs).KeyValue(registry.RegistryName)
	if err != nil {
		return err
	}

	// assign the KV handle so we can invoke methods from the registry package
	if err := registry.SetHandle(handle); err != nil && !errors.Is(err, registry.ErrRegistryPreviouslyInitialized) {
		return err
	}

	return nil
}

// controller registry helper methods
func (l *liveness) register(controllerID string) (registry.ControllerID, error) {
	id := registry.GetID(controllerID)
	le := l.logger.WithField("id", id.String())

	le.Info("registry controllerID assigned")
	if err := registry.RegisterController(id); err != nil {
		metrics.DependencyError("liveness", "register")
		le.WithError(err).Warn("initial controller liveness registration failed")

		return nil, err
	}

	return id, nil
}

func (l *liveness) checkin(id registry.ControllerID) error {
	le := l.logger.WithField("id", id.String())

	_, err := registry.LastContact(id)
	if err != nil {
		le.WithError(err).Warn("error identifying last contact from controller")
		return err
	}

	if errCheckin := registry.ControllerCheckin(id); errCheckin != nil {
		metrics.DependencyError("liveness", "check-in")
		le.WithError(errCheckin).Warn("controller checkin failed")
		// try to refresh our token, maybe this is a NATS hiccup
		//
		// TODO: get the client to retry
		if errRefresh := refreshControllerToken(id); errRefresh != nil {
			// couldn't refresh our liveness token, time to bail out
			le.WithError(errRefresh).Error("controller re-register required")
			return errors.Wrap(errRegistryCheckin, "check-in and refresh attempt failed, controller re-register required")
		}
	}

	return nil
}

// try to de-register/re-register this id.
func refreshControllerToken(id registry.ControllerID) error {
	err := registry.DeregisterController(id)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		metrics.DependencyError("liveness", "de-register")
		return err
	}
	err = registry.RegisterController(id)
	if err != nil {
		metrics.DependencyError("liveness", "register")
		return err
	}
	return nil
}
