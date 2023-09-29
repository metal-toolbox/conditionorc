package store

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	rctypes "github.com/metal-toolbox/rivets/condition"
	sservice "go.hollow.sh/serverservice/pkg/api/v1"
)

// Serverservice implements the Repository interface to have server condition objects stored as server attributes.
type Serverservice struct {
	config               *app.ServerserviceOptions
	conditionDefinitions rctypes.Definitions
	client               *sservice.Client
	logger               *logrus.Logger
}

var (
	// ErrServerserviceConfig is returned when theres an error in loading serverservice configuration.
	ErrServerserviceConfig = errors.New("Serverservice configuration error")

	// ErrServerserviceQuery is returned when a serverservice query error was received.
	ErrServerserviceQuery = errors.New("Serverservice query error")

	// ErrServserviceAttribute is returned when a serverservice attribute does not contain the expected fields.
	ErrServerserviceAttribute = errors.New("error in serverservice attribute")

	// ServerserviceConditionsNSFmtStr attribute namespace format string value for server condition attributes.
	ServerserviceConditionsNSFmtStr = "sh.hollow.rctypes.%s"

	pkgName = "internal/store"

	// connectionTimeout is the maximum amount of time spent on each http connection to serverservice.
	connectionTimeout = 30 * time.Second
)

func serverServiceError(operation string) {
	metrics.DependencyError("serverservice", operation)
}

func newServerserviceStore(ctx context.Context, config *app.ServerserviceOptions, conditionDefs rctypes.Definitions, logger *logrus.Logger) (Repository, error) {
	s := &Serverservice{logger: logger, conditionDefinitions: conditionDefs, config: config}

	var client *sservice.Client
	var err error

	if !config.DisableOAuth {
		client, err = newClientWithOAuth(ctx, config, logger)
		if err != nil {
			return nil, err
		}
	} else {
		client, err = sservice.NewClientWithToken("fake", config.Endpoint, nil)
		if err != nil {
			return nil, err
		}
	}

	s.client = client

	return s, nil
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

// Ping tests the repository is available.
func (s *Serverservice) Ping(_ context.Context) error {
	// TODO: implement
	return nil
}

// Get a condition set on a server.
// @id: required
// @conditionKind: required
func (s *Serverservice) Get(ctx context.Context, serverID uuid.UUID,
	conditionKind rctypes.Kind,
) (*rctypes.Condition, error) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.Get")
	defer span.End()
	// list attributes on a server
	attributes, _, err := s.client.GetAttributes(otelCtx, serverID, s.conditionNS(conditionKind))
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrConditionNotFound
		}
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     conditionKind,
			"error":    err,
			"method":   "Get",
		}).Warn("error reaching serverservice")
		serverServiceError("get-attributes")
		return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
	}

	if attributes == nil {
		// XXX: is this a realistic failure case?
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     conditionKind,
		}).Warn("malformed condition data")
		return nil, ErrMalformedCondition
	}

	// return condition object from attribute
	return s.conditionFromAttribute(attributes)
}

// GetServer gets attributes for a server.
// @serverID: required
// @conditionKind: required
func (s *Serverservice) GetServer(ctx context.Context, serverID uuid.UUID) (*model.Server, error) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.GetServer")
	defer span.End()

	// list attributes on a server
	obj, _, err := s.client.Get(otelCtx, serverID)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrConditionNotFound
		}

		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"error":    err,
			"method":   "GetServer",
		}).Warn("error reaching serverservice")

		serverServiceError("get-server")

		return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
	}

	return &model.Server{ID: obj.UUID, FacilityCode: obj.FacilityCode}, nil
}

// List all conditions set on a server.
// @id: required
// @conditionState: optional
func (s *Serverservice) List(ctx context.Context, serverID uuid.UUID,
	conditionState rctypes.State,
) ([]*rctypes.Condition, error) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.List")
	defer span.End()
	found := []*sservice.Attributes{}

	for _, def := range s.conditionDefinitions {
		attr, _, err := s.client.GetAttributes(otelCtx, serverID, s.conditionNS(def.Kind))
		if err != nil {
			if strings.Contains(err.Error(), "404") {
				continue
			}
			s.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"kind":     def.Kind,
				"error":    err,
				"method":   "List",
			}).Warn("error reaching serverservice")
			serverServiceError("list-conditions")
			return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
		}

		found = append(found, attr)
	}

	// list attributes on a server
	return s.findConditionByStateInAttributes(conditionState, found), nil
}

// Create a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition payload and to delete any existing condition before creating.
func (s *Serverservice) Create(ctx context.Context, serverID uuid.UUID, createCondition *rctypes.Condition) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.Create")
	defer span.End()
	createCondition.ResourceVersion = time.Now().UnixNano()

	payload, err := json.Marshal(createCondition)
	if err != nil {
		return errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	data := sservice.Attributes{
		Namespace: s.conditionNS(createCondition.Kind),
		Data:      payload,
	}

	_, err = s.client.CreateAttributes(otelCtx, serverID, data)
	if err != nil {
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     createCondition.Kind,
			"error":    err,
		}).Warn("error creating condition")
		serverServiceError("create-condition")
	}
	return err
}

// Update a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition update payload.
func (s *Serverservice) Update(ctx context.Context, serverID uuid.UUID, updateCondition *rctypes.Condition) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.Update")
	defer span.End()
	updateCondition.ResourceVersion = time.Now().UnixNano()

	payload, err := json.Marshal(updateCondition)
	if err != nil {
		return errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	_, err = s.client.UpdateAttributes(otelCtx, serverID, s.conditionNS(updateCondition.Kind), payload)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			s.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"kind":     updateCondition.Kind,
				"method":   "Update",
			}).Warn("no condition match for this server")
			return ErrConditionNotFound
		}
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     updateCondition.Kind,
			"error":    err,
		}).Warn("error updating condition")
		serverServiceError("update-condition")
	}
	return err
}

// Delete a condition from a server.
// @id: required
// @conditionKind: required
func (s *Serverservice) Delete(ctx context.Context, serverID uuid.UUID, conditionKind rctypes.Kind) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.Delete")
	defer span.End()
	_, err := s.client.DeleteAttributes(otelCtx, serverID, s.conditionNS(conditionKind))
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			s.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"kind":     conditionKind,
				"method":   "Delete",
			}).Warn("no condition match for this server")
			return ErrConditionNotFound
		}
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     conditionKind,
			"error":    err,
		}).Warn("error deleting condition")
		serverServiceError("delete-condition")
	}
	return err
}

// ListServersWithCondition lists servers with the given condition kind.
func (s *Serverservice) ListServersWithCondition(ctx context.Context,
	conditionKind rctypes.Kind, conditionState rctypes.State,
) ([]*rctypes.ServerConditions, error) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Serverservice.ListServersWithCondition")
	defer span.End()
	params := &sservice.ServerListParams{
		AttributeListParams: []sservice.AttributeListParams{
			{
				Namespace: s.conditionNS(conditionKind),
				Keys:      []string{"state"},
				Operator:  sservice.OperatorEqual,
				Value:     string(conditionState),
			},
		},
	}

	_, _, err := s.client.List(otelCtx, params)
	if err != nil {
		s.logger.WithFields(logrus.Fields{
			"kind":  conditionKind,
			"state": conditionState,
			"error": err,
		}).Warn("error listing servers")
		serverServiceError("list-servers-with-condition")
		return nil, err
	}

	return nil, nil
}
