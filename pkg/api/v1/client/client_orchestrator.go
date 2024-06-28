package client

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/metal-toolbox/rivets/events/registry"
)

// These client methods are for the Orchestrator API
// TODO: figure if we want to organize these differently
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
	} else {
		if statusValue == nil {
			statusValue = &rctypes.StatusValue{}
		}
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
		path += "&ts_update=true"
	} else {
		if task == nil {
			task = &rctypes.Task[any, any]{}
		}
	}

	return c.post(ctx, path, task)
}

func (c *Client) ConditionTaskQuery(ctx context.Context, conditionKind rctypes.Kind, serverID uuid.UUID) (*v1types.ServerResponse, error) {
	path := fmt.Sprintf("servers/%s/condition-task/%s", serverID.String(), conditionKind)

	return c.get(ctx, path)
}
