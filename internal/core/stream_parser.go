package core

import (
	"fmt"
	"strings"
)

const (
	modeThinking = iota
	modeChat
	modeTool
	modeCode
)

type StreamParser struct {
	markerChat string
	markerTool string
	markerCode string

	enforcementSuffix string

	fullBuffer strings.Builder
	chatPos    int
}

func NewStreamParser(chat, tool, code string) *StreamParser {
	return &StreamParser{
		markerChat: chat,
		markerTool: tool,
		markerCode: code,
		enforcementSuffix: fmt.Sprintf("\n\n[CRITICAL: IF tools are needed, follow the EXACT protocol: %s (Friendly confirmation), %s (Valid JSON parameters), %s (Full source code). NO MARKDOWN!]",
			chat, tool, code),
	}
}

// ProcessToken handles a single token. It streams chat content live.
func (p *StreamParser) ProcessToken(token string) (chatContent string) {
	p.fullBuffer.WriteString(token)
	accumulated := p.fullBuffer.String()

	// 1. Identify where everything starts and stops
	chatStart := strings.Index(accumulated, p.markerChat)
	toolStart := strings.Index(accumulated, p.markerTool)
	codeStart := strings.Index(accumulated, p.markerCode)

	// Determine where we should stop streaming chat
	chatStopIdx := len(accumulated)
	if toolStart != -1 {
		chatStopIdx = toolStart
	} else if codeStart != -1 {
		chatStopIdx = codeStart
	} else {
		// Only hold back if the tail strictly looks like a Vraxter marker [VRAX_...
		// This prevents holding back simple conversational [ brackets.
		tail := accumulated[p.chatPos:]
		if strings.Contains(tail, "[") {
			// Find the last '['
			lastBracket := strings.LastIndex(tail, "[")
			possibleMarker := tail[lastBracket:]
			// If it's a prefix of any marker, hold it back
			for _, m := range []string{p.markerChat, p.markerTool, p.markerCode} {
				if strings.HasPrefix(m, possibleMarker) {
					return ""
				}
			}
		}
	}

	// Determine where we should start streaming from
	var streamFrom int
	if chatStart != -1 {
		streamFrom = chatStart + len(p.markerChat)
		// Skip optional colon and spacing that models love to add
		for streamFrom < len(accumulated) && (accumulated[streamFrom] == ':' || accumulated[streamFrom] == ' ') {
			streamFrom++
		}
	} else {
		// Native chat mode: start from the beginning
		streamFrom = 0
	}

	if p.chatPos < streamFrom {
		p.chatPos = streamFrom
	}

	if p.chatPos < chatStopIdx {
		chunk := accumulated[p.chatPos:chatStopIdx]
		p.chatPos = chatStopIdx
		return chunk
	}

	return ""
}

func (p *StreamParser) ToolPayload() string {
	full := p.fullBuffer.String()
	toolStart := strings.Index(full, p.markerTool)
	if toolStart == -1 {
		return ""
	}
	toolStart += len(p.markerTool)
	// Skip optional colon and spacing
	for toolStart < len(full) && (full[toolStart] == ':' || full[toolStart] == ' ' || full[toolStart] == '\n') {
		toolStart++
	}

	// Find the FIRST marker that appears AFTER toolStart to act as the boundary
	nextBoundary := len(full)
	for _, marker := range []string{p.markerCode, p.markerChat} {
		if idx := strings.Index(full[toolStart:], marker); idx != -1 {
			if toolStart+idx < nextBoundary {
				nextBoundary = toolStart + idx
			}
		}
	}

	return strings.TrimSpace(full[toolStart:nextBoundary])
}

func (p *StreamParser) CodePayload() string {
	full := p.fullBuffer.String()
	codeStart := strings.Index(full, p.markerCode)
	if codeStart == -1 {
		return ""
	}
	codeStart += len(p.markerCode)
	// Skip optional colon and spacing
	for codeStart < len(full) && (full[codeStart] == ':' || full[codeStart] == ' ' || full[codeStart] == '\n') {
		codeStart++
	}
	// Find the FIRST marker that appears AFTER codeStart to act as the boundary
	nextBoundary := len(full)
	for _, marker := range []string{p.markerTool, p.markerChat} {
		if idx := strings.Index(full[codeStart:], marker); idx != -1 {
			if codeStart+idx < nextBoundary {
				nextBoundary = codeStart + idx
			}
		}
	}
	return strings.TrimSpace(full[codeStart:nextBoundary])
}

func (p *StreamParser) State() int { return 0 }

// Flush emits any remaining buffered content that the safety margin was holding.
// Must be called when the stream is fully done (EventTypeDone).
func (p *StreamParser) Flush() string {
	full := p.fullBuffer.String()

	chatStart := strings.Index(full, p.markerChat)
	var from int
	if chatStart != -1 {
		from = chatStart + len(p.markerChat)
		// Skip optional colon and spacing
		for from < len(full) && (full[from] == ':' || full[from] == ' ') {
			from++
		}
	} else {
		from = 0
	}

	if p.chatPos < from {
		p.chatPos = from
	}

	if p.chatPos < len(full) {
		remaining := full[p.chatPos:]
		p.chatPos = len(full)
		// Strip any VRAX markers that sneaked in at the tail
		if idx := strings.Index(remaining, "[VRAX"); idx != -1 {
			remaining = remaining[:idx]
		}
		return remaining
	}
	return ""
}
