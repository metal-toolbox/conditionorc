package fleetdb

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"

	fleetdbapi "github.com/metal-toolbox/fleetdb/pkg/api/v1"
	rctypes "github.com/metal-toolbox/rivets/condition"
	rfleetdbapi "github.com/metal-toolbox/rivets/fleetdb"
)

const (
	secretSlug = "bmc"
)

type fleetDBImpl struct {
	config *app.FleetDBAPIOptions
	client *fleetdbapi.Client
	logger *logrus.Logger
}

type serverCreateStatus struct {
	serverCreated     bool
	credentialCreated bool
	attributesCreated bool
}

var (
	// connectionTimeout is the maximum amount of time spent on each http connection to FleetDBClient.
	connectionTimeout = 30 * time.Second
	pkgName           = "internal/fleetdb"
	errServerLookup   = errors.New("unable to retrieve server")
	ErrServerNotFound = errors.New("server not found")
)

func fleetDBError(operation string) {
	metrics.DependencyError("fleetDB", operation)
}

// AddServer creates a server record in FleetDB
func (s *fleetDBImpl) AddServer(ctx context.Context, serverID uuid.UUID, facilityCode, bmcAddr, bmcUser, bmcPass string) (func() error, error) {
	var createStatus serverCreateStatus
	cleanup := func() error {
		if createStatus.serverCreated {
			server := fleetdbapi.Server{UUID: serverID, Name: serverID.String(), FacilityCode: facilityCode}
			_, err := s.client.Delete(ctx, server)
			if err != nil {
				s.logger.WithError(err).Warning("server enroll failed to rollback server")
				return err
			}
		}

		if createStatus.credentialCreated {
			_, err := s.client.DeleteCredential(ctx, serverID, secretSlug)
			if err != nil {
				s.logger.WithError(err).Warning("server enroll failed to rollback credential")
				return err
			}
		}

		if createStatus.attributesCreated {
			_, err := s.client.DeleteAttributes(ctx, serverID, rfleetdbapi.ServerAttributeNSBmcAddress)
			if err != nil {
				s.logger.WithError(err).Warning("server enroll failed to rollback attributes")
				return err
			}
		}
		return nil
	}
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "FleetDB.AddServer")
	defer span.End()

	if bmcUser == "" || bmcPass == "" {
		return cleanup, ErrBMCCredentials
	}

	addr, err := netip.ParseAddr(bmcAddr)
	if err != nil {
		return cleanup, err
	}

	// Add server
	server := fleetdbapi.Server{UUID: serverID, Name: serverID.String(), FacilityCode: facilityCode}
	_, _, err = s.client.Create(otelCtx, server)
	if err != nil {
		fleetDBError("create-server")
		return cleanup, err
	}
	createStatus.serverCreated = true

	// Add server BMC credential
	_, err = s.client.SetCredential(otelCtx, serverID, secretSlug, bmcUser, bmcPass)
	if err != nil {
		fleetDBError("set-credentials")
		return cleanup, err
	}
	createStatus.credentialCreated = true

	// Add server BMC IP attribute
	addrAttr := fmt.Sprintf(`{"address": %q}`, addr.String())
	bmcIPAttr := fleetdbapi.Attributes{Namespace: rfleetdbapi.ServerAttributeNSBmcAddress, Data: []byte(addrAttr)}
	_, err = s.client.CreateAttributes(otelCtx, serverID, bmcIPAttr)
	if err != nil {
		fleetDBError("create-bmc-ip-attributes")
		return cleanup, err
	}
	createStatus.attributesCreated = true
	return cleanup, nil
}

// GetServer returns the facility for the requested server id.
func (s *fleetDBImpl) GetServer(ctx context.Context, serverID uuid.UUID) (*model.Server, error) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "FleetDB.GetServer")
	defer span.End()

	// list attributes on a server
	obj, _, err := s.client.Get(otelCtx, serverID)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrServerNotFound
		}

		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"error":    err,
			"method":   "GetServer",
		}).Warn("error reaching fleetDB")

		fleetDBError("get-server")

		return nil, errors.Wrap(errServerLookup, err.Error())
	}

	return &model.Server{ID: obj.UUID, FacilityCode: obj.FacilityCode}, nil
}

// DeleteServer creates a server record in FleetDB
func (s *fleetDBImpl) DeleteServer(ctx context.Context, serverID uuid.UUID) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "FleetDB.GetServer")
	defer span.End()

	_, err := s.client.Delete(otelCtx, fleetdbapi.Server{UUID: serverID})
	if err != nil {
		fleetDBError("delete-server")
	}
	return err
}

// WriteConditionHistory adds an event-history record to FleetDB describing the outcome of this condition.
// XXX: only call this when the condition is complete. FleetDB only tracks the final outcome.
func (i *fleetDBImpl) WriteConditionHistory(ctx context.Context, cond *rctypes.Condition) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "FleetDB.UpdateEvent")
	defer span.End()

	payload := &fleetdbapi.Event{
		EventID:     cond.ID,
		Type:        string(cond.Kind),
		Start:       cond.CreatedAt,
		End:         cond.UpdatedAt,
		Target:      cond.Target,
		FinalState:  string(cond.State),
		FinalStatus: cond.Status,
		Parameters:  cond.Parameters,
	}

	_, err := i.client.UpdateEvent(otelCtx, payload)
	if err != nil {
		fleetDBError("update-event-history")
		i.logger.WithFields(logrus.Fields{
			"condition.id": cond.ID.String(),
			"server.id":    cond.Target.String(),
			"error":        err,
			"method":       "UpdateEvent",
		}).Warn("updating event history")
	}
	return err
}
