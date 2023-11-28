package fleetdb

import (
	"context"
	"net/url"

	"github.com/coreos/go-oidc"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/model"
	rctypes "github.com/metal-toolbox/rivets/condition"
	sservice "go.hollow.sh/serverservice/pkg/api/v1"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/sirupsen/logrus"
)

// FleetDB handles traffics between conditionorc and fleet db.
// NOTE: when updating this interface, run make gen-store-mock to make sure the mocks are updated.
type FleetDB interface {
	// AddServer creates a server in fleet db.
	AddServer(ctx context.Context, serverID uuid.UUID, facilityCode, bmcAddr, bmcUser, bmcPass string) (func() error, error)
	// Get Server attributes.
	// @serverID: required
	GetServer(ctx context.Context, serverID uuid.UUID) (*model.Server, error)
	// DeleteServer
	DeleteServer(ctx context.Context, serverID uuid.UUID) error
}

func NewFleetDBClient(ctx context.Context, config *app.Configuration, conditionDefs rctypes.Definitions,
	logger *logrus.Logger) (FleetDB, error) {

	ssOpts := &config.ServerserviceOptions
	client, err := getServerServiceClient(ctx, ssOpts, logger)
	if err != nil {
		return nil, err
	}
	return &fleetDBImpl{
		config:               ssOpts,
		conditionDefinitions: conditionDefs,
		logger:               logger,
		client:               client,
	}, nil
}

func getServerServiceClient(ctx context.Context, cfg *app.ServerserviceOptions, log *logrus.Logger) (*sservice.Client, error) {
	var client *sservice.Client
	var err error

	if cfg.DisableOAuth {
		client, err = sservice.NewClientWithToken("fake", cfg.Endpoint, nil)
		if err != nil {
			return nil, err
		}
	} else {
		client, err = newClientWithOAuth(ctx, cfg, log)
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}

// returns a serverservice retryable http client with Otel and Oauth wrapped in
func newClientWithOAuth(ctx context.Context, cfg *app.ServerserviceOptions, logger *logrus.Logger) (*sservice.Client, error) {
	// init retryable http client
	retryableClient := retryablehttp.NewClient()

	// set retryable HTTP client to be the otel http client to collect telemetry
	retryableClient.HTTPClient = otelhttp.DefaultClient

	// disable default debug logging on the retryable client
	if logger.Level < logrus.DebugLevel {
		retryableClient.Logger = nil
	} else {
		retryableClient.Logger = logger
	}

	// setup oidc provider
	provider, err := oidc.NewProvider(ctx, cfg.OidcIssuerEndpoint)
	if err != nil {
		return nil, err
	}

	clientID := "conditionorc-api"

	if cfg.OidcClientID != "" {
		clientID = cfg.OidcClientID
	}

	// setup oauth configuration
	oauthConfig := clientcredentials.Config{
		ClientID:       clientID,
		ClientSecret:   cfg.OidcClientSecret,
		TokenURL:       provider.Endpoint().TokenURL,
		Scopes:         cfg.OidcClientScopes,
		EndpointParams: url.Values{"audience": []string{cfg.OidcAudienceEndpoint}},
		// with this the oauth client spends less time identifying the client grant mechanism.
		AuthStyle: oauth2.AuthStyleInParams,
	}

	// wrap OAuth transport, cookie jar in the retryable client
	oAuthclient := oauthConfig.Client(ctx)

	retryableClient.HTTPClient.Transport = oAuthclient.Transport
	retryableClient.HTTPClient.Jar = oAuthclient.Jar

	httpClient := retryableClient.StandardClient()
	httpClient.Timeout = connectionTimeout

	return sservice.NewClientWithToken(
		cfg.OidcClientSecret,
		cfg.Endpoint,
		httpClient,
	)
}
