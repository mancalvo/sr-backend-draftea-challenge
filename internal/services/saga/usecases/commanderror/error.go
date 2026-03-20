package commanderror

// Error represents an application error that adapters can map to a response.
type Error struct {
	status  int
	code    string
	message string
}

// New creates a new command error.
func New(status int, message, code string) *Error {
	return &Error{
		status:  status,
		code:    code,
		message: message,
	}
}

func (e *Error) Error() string {
	return e.message
}

// HTTPStatus returns the transport status code.
func (e *Error) HTTPStatus() int {
	return e.status
}

// HTTPCode returns the optional machine-readable error code.
func (e *Error) HTTPCode() string {
	return e.code
}

// HTTPMessage returns the user-facing error message.
func (e *Error) HTTPMessage() string {
	return e.message
}
