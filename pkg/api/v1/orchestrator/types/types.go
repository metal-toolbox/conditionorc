package types

import (
	rctypes "github.com/metal-toolbox/rivets/v2/condition"
)

type ServerResponse struct {
	StatusCode int                     `json:"statusCode,omitempty"`
	Message    string                  `json:"message,omitempty"`
	Condition  *rctypes.Condition      `json:"condition,omitempty"`
	Task       *rctypes.Task[any, any] `json:"task,omitempty"`
}
