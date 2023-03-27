package client

// Error holds the cause of a client error and implements the Error interface.
type Error struct {
	Cause string
}

// Error returned for a client side problem.
func (c Error) Error() string {
	return "conditionorc client error - " + c.Cause
}

// RequestError is returned when the client gets an error while performing a request.
type RequestError struct {
	Message    string `json:"message"`
	StatusCode int    `json:"stateusCode"`
}

// Error returns the RequestError in string format
func (e RequestError) Error() string {
	return "conditionorc client request error: " + e.Message
}
