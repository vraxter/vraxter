package tests

import (
	"errors"
	"testing"

	"github.com/patagonicrune/vraxter/pkg/vraxerror"
)

func TestVraxError_New(t *testing.T) {
	sentinel := errors.New("disk full")
	err := vraxerror.New(vraxerror.ErrTypeInternal, "storage layer failed", false, sentinel)

	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if err.Type != vraxerror.ErrTypeInternal {
		t.Errorf("expected type %q, got %q", vraxerror.ErrTypeInternal, err.Type)
	}
	if err.Message != "storage layer failed" {
		t.Errorf("unexpected message: %q", err.Message)
	}
	if err.Recoverable {
		t.Error("expected Recoverable=false")
	}
}

func TestVraxError_Error_WithUnderlyingError(t *testing.T) {
	inner := errors.New("connection refused")
	err := vraxerror.New(vraxerror.ErrTypeNetwork, "llm endpoint unreachable", true, inner)

	msg := err.Error()
	if msg == "" {
		t.Fatal("Error() should not be empty")
	}
	// Should contain type, message, and the underlying error
	for _, expected := range []string{"network", "llm endpoint unreachable", "connection refused"} {
		if !containsStr(msg, expected) {
			t.Errorf("Error() missing %q in output: %q", expected, msg)
		}
	}
}

func TestVraxError_Error_WithoutUnderlyingError(t *testing.T) {
	err := vraxerror.New(vraxerror.ErrTypeParse, "invalid json block", false, nil)
	msg := err.Error()
	if msg == "" {
		t.Fatal("Error() should not be empty even without underlying error")
	}
	if !containsStr(msg, "parse") {
		t.Errorf("expected 'parse' in error string, got: %q", msg)
	}
}

func TestVraxError_IsRecoverable_True(t *testing.T) {
	err := vraxerror.New(vraxerror.ErrTypeNetwork, "timeout", true, nil)
	if !vraxerror.IsRecoverable(err) {
		t.Error("expected IsRecoverable=true")
	}
}

func TestVraxError_IsRecoverable_False(t *testing.T) {
	err := vraxerror.New(vraxerror.ErrTypeSandbox, "wasm crash", false, nil)
	if vraxerror.IsRecoverable(err) {
		t.Error("expected IsRecoverable=false")
	}
}

func TestVraxError_IsRecoverable_UnknownError(t *testing.T) {
	plainErr := errors.New("some random error")
	// Unknown errors default to non-recoverable
	if vraxerror.IsRecoverable(plainErr) {
		t.Error("expected IsRecoverable=false for plain error")
	}
}

func TestVraxError_ImplementsErrorInterface(t *testing.T) {
	var err error = vraxerror.New(vraxerror.ErrTypeInternal, "test", false, nil)
	if err == nil {
		t.Fatal("EngineError must satisfy error interface")
	}
}

func TestVraxError_AllErrorTypes(t *testing.T) {
	types := []vraxerror.ErrorType{
		vraxerror.ErrTypeNetwork,
		vraxerror.ErrTypeInternal,
		vraxerror.ErrTypeParse,
		vraxerror.ErrTypeSandbox,
		vraxerror.ErrTypeTimeout,
	}
	for _, et := range types {
		e := vraxerror.New(et, "test message", false, nil)
		if e.Type != et {
			t.Errorf("expected type %q, got %q", et, e.Type)
		}
	}
}

// helper
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
