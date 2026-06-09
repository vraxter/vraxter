package core

import (
	"log/slog"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/pkg/types"
)

type ContextManager struct {
	ChatRepo *db.ChatRepository
}

func NewContextManager(repo *db.ChatRepository) *ContextManager {
	return &ContextManager{ChatRepo: repo}
}

// PruneHistory dynamically fetches and prunes history based on the token limit of the current model.
// This preserves context for long coding sessions without running out of tokens.
func (cm *ContextManager) PruneHistory(conversationID string, contextWindow int) []types.Message {
	// If contextWindow is 0 (legacy or unconfigured), fallback to safe limit
	if contextWindow <= 0 {
		contextWindow = 8192
	}

	// We reserve 40% of the context window for the System Prompt and Output Generation
	tokenLimit := int(float64(contextWindow) * 0.6)

	// Fetch a large buffer of messages
	allMsgs, err := cm.ChatRepo.GetMessagesByConversation(conversationID, 100)
	if err != nil || len(allMsgs) == 0 {
		return []types.Message{}
	}

	var pruned []types.Message
	currentTokens := 0

	// Iterate backwards (newest to oldest)
	for i := len(allMsgs) - 1; i >= 0; i-- {
		msg := allMsgs[i]
		// Rough token estimation: 1 token ~= 4 characters
		estimatedTokens := len(msg.Content) / 4
		if estimatedTokens == 0 {
			estimatedTokens = 1
		}

		if currentTokens+estimatedTokens > tokenLimit {
			slog.Info("Context window limit reached, truncating older messages", "allowed", tokenLimit, "used", currentTokens)
			break
		}

		currentTokens += estimatedTokens
		// Prepend to maintain chronological order
		pruned = append([]types.Message{msg}, pruned...)
	}

	return pruned
}
