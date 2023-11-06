package fleetdb

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/sirupsen/logrus"

	rctypes "github.com/metal-toolbox/rivets/condition"
	rservice "github.com/metal-toolbox/rivets/serverservice"
	sservice "go.hollow.sh/serverservice/pkg/api/v1"
)

type fleetDBImpl struct {
	config               *app.ServerserviceOptions
	conditionDefinitions rctypes.Definitions
	client               *sservice.Client
	logger               *logrus.Logger
}

var (
	// connectionTimeout is the maximum amount of time spent on each http connection to FleetDBClient.
	connectionTimeout = 30 * time.Second
)

// AddServer creates a server record in FleetDB
func (s *fleetDBImpl) AddServer(ctx context.Context, serverID uuid.UUID, facilityCode, bmcAddr, bmcUser, bmcPass string) error {
	if bmcUser == "" || bmcPass == "" {
		return ErrBMCCredentials
	}

	addr, err := netip.ParseAddr(bmcAddr)
	if err != nil {
		return err
	}

	// Add server
	server := sservice.Server{UUID: serverID, Name: serverID.String(), FacilityCode: facilityCode}
	_, _, err = s.client.Create(ctx, server)
	if err != nil {
		return err
	}

	// Add server BMC credential
	_, err = s.client.SetCredential(ctx, serverID, "bmc", bmcUser, bmcPass)
	if err != nil {
		return err
	}

	// Add server BMC IP attribute
	addrAttr := fmt.Sprintf(`{"address": %q}`, addr.String())
	bmcIPAttr := sservice.Attributes{Namespace: rservice.ServerAttributeNSBmcAddress, Data: []byte(addrAttr)}
	_, err = s.client.CreateAttributes(ctx, serverID, bmcIPAttr)
	return err
}
