package store

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
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// NOTE: when updating this interface, run make gen-store-mock to make sure the mocks are updated.
type Repository interface {
	// Get a condition set on a server.
	// @serverID: required
	// @conditionKind: required
	Get(ctx context.Context, serverID uuid.UUID, conditionKind rctypes.Kind) (*rctypes.Condition, error)

	// Get Server attributes.
	// @serverID: required
	GetServer(ctx context.Context, serverID uuid.UUID) (*model.Server, error)

	// List all conditions set on a server.
	// @serverID: required
	// @conditionState: optional
	List(ctx context.Context, serverID uuid.UUID, conditionState rctypes.State) ([]*rctypes.Condition, error)

	// @serverID: required
	// @condition: required
	Create(ctx context.Context, serverID uuid.UUID, condition *rctypes.Condition) error

	// Update a condition on a server
	// @serverID: required
	// @condition: required
	Update(ctx context.Context, serverID uuid.UUID, condition *rctypes.Condition) error

	// Delete a condition from a server.
	// @serverID: required
	// @conditionKind: required
	Delete(ctx context.Context, serverID uuid.UUID, conditionKind rctypes.Kind) error
}

var ErrRepository = errors.New("storage repository error")

func NewStore(ctx context.Context, config *app.Configuration, conditionDefs rctypes.Definitions,
	logger *logrus.Logger, stream events.Stream) (Repository, error) {

	ssOpts := &config.ServerserviceOptions
	client, err := getServerServiceClient(ctx, ssOpts, logger)
	if err != nil {
		return nil, err
	}

	switch config.StoreKind {
	case model.ServerserviceStore:
		return &Serverservice{
			config:               ssOpts,
			conditionDefinitions: conditionDefs,
			logger:               logger,
			client:               client,
		}, nil
	case model.NATS:
		return newNatsRepository(client, logger, stream, config.NatsOptions.KVReplicationFactor)
	default:
		return nil, errors.Wrap(ErrRepository, "storage kind not implemented: "+string(config.StoreKind))
	}
}

func getServerServiceClient(ctx context.Context, cfg *app.ServerserviceOptions, log *logrus.Logger) (*sservice.Client, error) {
	var client *sservice.Client
	var err error

	if !cfg.DisableOAuth {
		client, err = newClientWithOAuth(ctx, cfg, log)
		if err != nil {
			return nil, err
		}
	} else {
		client, err = sservice.NewClientWithToken("fake", cfg.Endpoint, nil)
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
