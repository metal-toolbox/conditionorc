package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	condition "github.com/metal-toolbox/rivets/condition"
)

// Doer performs HTTP requests.
//
// The standard http.Client implements this interface.
type HTTPRequestDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client can perform queries against the conditionorc API service.
type Client struct {
	// The server address with the schema - https://foo.com
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

func (c *Client) ServerConditionGet(ctx context.Context, serverID uuid.UUID, conditionKind condition.Kind) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition/%s", serverID.String(), conditionKind)

	return c.get(ctx, path)
}

func (c *Client) ServerConditionList(ctx context.Context, serverID uuid.UUID, conditionState condition.State) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/state/%s", serverID.String(), conditionState)

	return c.get(ctx, path)
}

func (c *Client) ServerConditionCreate(ctx context.Context, serverID uuid.UUID, conditionKind condition.Kind, conditionCreate v1types.ConditionCreate) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition/%s", serverID.String(), conditionKind)

	return c.post(ctx, path, conditionCreate)
}

//nolint:gocritic // 80 bytes for v1types.ConditionUpdate is big, but not *huge*
func (c *Client) ServerConditionUpdate(ctx context.Context, serverID uuid.UUID, conditionKind condition.Kind, conditionUpdate v1types.ConditionUpdate) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition/%s", serverID.String(), conditionKind)

	return c.put(ctx, path, conditionUpdate)
}

func (c *Client) ServerConditionDelete(ctx context.Context, serverID uuid.UUID, conditionKind condition.Kind) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition/%s", serverID.String(), conditionKind)

	return c.delete(ctx, path)
}
