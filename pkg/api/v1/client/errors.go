package client

import "fmt"

// ClientError is returned when invalid arguments are provided to the client
type ClientError struct {
	Message string
}

// Error returns the ClientError in string format
func (e *ClientError) Error() string {
	return fmt.Sprintf("hollow client error: %s", e.Message)
}

// ServerError is returned when the client receives an error back from the server
type ServerError struct {
	Message      string `json:"message"`
	ErrorMessage string `json:"error"`
	StatusCode   int
}

// Error returns the ServerError in string format
func (e ServerError) Error() string {
	return fmt.Sprintf("hollow client received a server error - response code: %d, message: %s, details: %s", e.StatusCode, e.Message, e.ErrorMessage)
}

func newClientError(msg string) *ClientError {
	return &ClientError{
		Message: msg,
	}
}
