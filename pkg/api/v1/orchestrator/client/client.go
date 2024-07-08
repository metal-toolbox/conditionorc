package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/metal-toolbox/rivets/events/registry"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

// Doer performs HTTP requests.
//
// The standard http.Client implements this interface.
type HTTPRequestDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client can perform queries against the Conditions Orchestrator API service.
type Client struct {
	// The Orchestrator API server address with the schema - https://foo.com
	serverAddress string

	// Authentication token
	authToken string

	// Doer for performing requests, typically a *http.Client with any
	// customized settings, such as certificate chains.
	client HTTPRequestDoer
}

// Option allows setting custom parameters during construction
type Option func(*Client) error

// Creates a new Client, with reasonable defaults
func NewClient(serverAddress string, opts ...Option) (*Client, error) {
	// create a client with sane default values
	client := Client{
		serverAddress: serverAddress,
	}
	// mutate client and add all optional params
	for _, o := range opts {
		if err := o(&client); err != nil {
			return nil, err
		}
	}

	// create httpClient, if not already present
	if client.client == nil {
		client.client = &http.Client{}
	}

	return &client, nil
}

// WithHTTPClient allows overriding the default Doer, which is
// automatically created using http.Client. This is useful for tests.
func WithHTTPClient(doer HTTPRequestDoer) Option {
	return func(c *Client) error {
		c.client = doer
		return nil
	}
}

// WithAuthToken sets the client auth token.
func WithAuthToken(authToken string) Option {
	return func(c *Client) error {
		c.authToken = authToken
		return nil
	}
}

func (c *Client) ConditionStatusUpdate(ctx context.Context, conditionKind rctypes.Kind, serverID, conditionID uuid.UUID, controllerID registry.ControllerID, statusValue *rctypes.StatusValue, onlyUpdateTimestamp bool) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf(
		"servers/%s/condition-status/%s/%s?controller_id=%s",
		serverID.String(),
		conditionKind,
		conditionID.String(),
		controllerID.String(),
	)

	if onlyUpdateTimestamp {
		path += "&ts_update=true"
	} else if statusValue == nil {
		statusValue = &rctypes.StatusValue{}
	}

	return c.put(ctx, path, statusValue)
}

func (c *Client) ConditionQueuePop(ctx context.Context, conditionKind rctypes.Kind, serverID uuid.UUID) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition-queue/%s", serverID.String(), conditionKind)

	return c.get(ctx, path)
}

func (c *Client) ControllerCheckin(ctx context.Context, serverID, conditionID uuid.UUID, controllerID registry.ControllerID) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/controller-checkin/%s?controller_id=%s", serverID.String(), conditionID.String(), controllerID.String())

	return c.get(ctx, path)
}

func (c *Client) ConditionTaskPublish(ctx context.Context, conditionKind rctypes.Kind, serverID, conditionID uuid.UUID, task *rctypes.Task[any, any], onlyUpdateTimestamp bool) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition-task/%s/%s", serverID.String(), conditionKind, conditionID.String())

	if onlyUpdateTimestamp {
		path += "?ts_update=true"
	}

	return c.post(ctx, path, task)
}

func (c *Client) ConditionTaskQuery(ctx context.Context, conditionKind rctypes.Kind, serverID uuid.UUID) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition-task/%s", serverID.String(), conditionKind)

	return c.get(ctx, path)
}
