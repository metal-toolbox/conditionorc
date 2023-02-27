package client

// License
//
// Major portions of this code was adapted from the hollow-serverservice project
//
// https://github.com/metal-toolbox/hollow-serverservice/blob/main/LICENSE

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	apiv1 "github.com/metal-toolbox/conditionorc/pkg/api/v1/routes"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

const (
	conditionsEndpoint = "conditions"
)

// Create will attempt to create a server in Hollow and return the new server's UUID
//func (c *Client) Create(ctx context.Context, srv Server) (*uuid.UUID, *ServerResponse, error) {
//	resp, err := c.post(ctx, conditionsEndpoint, srv)
//	if err != nil {
//		return nil, nil, err
//	}
//
//	u, err := uuid.Parse(resp.Slug)
//	if err != nil {
//		return nil, resp, nil
//	}
//
//	return &u, resp, nil
//}
//
//// Delete will attempt to delete a server in Hollow and return an error on failure
//func (c *Client) Delete(ctx context.Context, srv Server) (*ServerResponse, error) {
//	return c.delete(ctx, fmt.Sprintf("%s/%s", serversEndpoint, srv.UUID))
//}

// Get will return the condition set on a server
func (c *Client) Get(ctx context.Context, serverID uuid.UUID, conditionKind ptypes.ConditionKind) (*ptypes.Condition, *apiv1.ServerResponse, error) {
	path := fmt.Sprintf("%s/%s", conditionsEndpoint, serverID)

	condition := &ptypes.Condition{}

	r := apiv1.ServerResponse{Data: condition}

	if err := c.get(ctx, path, &r); err != nil {
		return nil, nil, err
	}

	return condition, &r, nil
}

// List will return all servers with optional params to filter the results
//func (c *Client) List(ctx context.Context, params *ServerListParams) ([]Server, *ServerResponse, error) {
//	servers := &[]Server{}
//	r := ServerResponse{Records: servers}
//
//	if err := c.list(ctx, serversEndpoint, params, &r); err != nil {
//		return nil, nil, err
//	}
//
//	return *servers, &r, nil
//}
//
//// Update will to update a server with the new values passed in
//func (c *Client) Update(ctx context.Context, srvUUID uuid.UUID, srv Server) (*ServerResponse, error) {
//	path := fmt.Sprintf("%s/%s", serversEndpoint, srvUUID)
//	return c.put(ctx, path, srv)
//}
