package apiv1

type ServerResponse struct {
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"record,omitempty"`
}


