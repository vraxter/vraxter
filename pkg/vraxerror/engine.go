package vraxerror

import "fmt"

// ErrorType identifies the high-level category of an engine error.
type ErrorType string

const (
	ErrTypeNetwork  ErrorType = "network"
	ErrTypeInternal ErrorType = "internal"
	ErrTypeParse    ErrorType = "parse"
	ErrTypeSandbox  ErrorType = "sandbox"
	ErrTypeTimeout  ErrorType = "timeout"
)

// EngineError represents a standardized, system-wide error model supporting recoverability signals.
type EngineError struct {
	Type        ErrorType
	Message     string
	Recoverable bool
	Err         error // Underlying origin error
}

// Error implements the standard Go error interface.
func (e *EngineError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Type, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// Ensure EngineError implements the error interface exactly.
var _ error = (*EngineError)(nil)

// IsRecoverable checks if the provided generalized error implements the EngineError contract allowing safe system resets.
func IsRecoverable(err error) bool {
	if engineErr, ok := err.(*EngineError); ok {
		return engineErr.Recoverable
	}
	// Default to false for unknown errors
	return false
}

// New creates a new formatted EngineError easily.
func New(errType ErrorType, message string, recoverable bool, err error) *EngineError {
	return &EngineError{
		Type:        errType,
		Message:     message,
		Recoverable: recoverable,
		Err:         err,
	}
}
