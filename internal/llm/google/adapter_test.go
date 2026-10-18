package google

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/pkg/interfaces"
)

func TestBuildGeminiPayloadSkipsEmptyMessages(t *testing.T) {
	msgs := []interfaces.Message{
		{Role: "system", Content: "You are Vraxter."},
		{Role: "system", Content: "   "},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "  \n  "},
		{Role: "skill", Content: "Invoked test-skill"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "Next question"},
	}

	payload := buildGeminiPayload(msgs)
	if len(payload.Contents) != 3 {
		t.Fatalf("expected 3 non-empty chat messages in contents, got %d", len(payload.Contents))
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(b)
	if strings.Contains(jsonStr, `"parts":[{}]`) || strings.Contains(jsonStr, `{}`) {
		t.Errorf("serialized payload contains empty part object {}: %s", jsonStr)
	}
}

