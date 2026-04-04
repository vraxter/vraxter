package core

import (
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

	fullBuffer strings.Builder
	chatPos    int
}

func NewStreamParser(chat, tool, code string) *StreamParser {
	return &StreamParser{
		markerChat: chat,
		markerTool: tool,
		markerCode: code,
	}
}

// ProcessToken handles a single token. It streams chat content live.
func (p *StreamParser) ProcessToken(token string) (chatContent string) {
	p.fullBuffer.WriteString(token)
	accumulated := p.fullBuffer.String()

	// Find where CHAT starts
	chatStart := strings.Index(accumulated, p.markerChat)
	if chatStart == -1 {
		// No VRAX_CHAT marker — this is a native chat response.
		// Only hold back if the tail looks like it could be building a marker (has a '<').
		tail := accumulated[p.chatPos:]
		if strings.Contains(tail, "<") {
			// Hold — might be building <<<VRAX_CHAT>>>
			return ""
		}
		if len(tail) == 0 {
			return ""
		}
		p.chatPos = len(accumulated)
		return tail
	}

	chatStart += len(p.markerChat)

	// If there's a TOOL marker, we stop streaming chat there.
	toolStart := strings.Index(accumulated, p.markerTool)
	if toolStart != -1 && p.chatPos >= toolStart {
		return "" // Tool has started, silent mode
	}

	endIdx := len(accumulated)
	if toolStart != -1 {
		endIdx = toolStart
	} else {
		// Only hold back if the tail could be forming a <<<VRAX marker
		tail := accumulated[p.chatPos:]
		if strings.Contains(tail, "<") {
			// Find the '<' and only emit up to it
			angIdx := strings.LastIndex(accumulated, "<")
			if angIdx > p.chatPos {
				endIdx = angIdx
			} else {
				return ""
			}
		}
	}

	if p.chatPos < chatStart {
		p.chatPos = chatStart
	}

	if p.chatPos < endIdx {
		chunk := accumulated[p.chatPos:endIdx]
		p.chatPos = endIdx
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

	codeStart := strings.Index(full, p.markerCode)
	if codeStart == -1 {
		return full[toolStart:]
	}
	return full[toolStart:codeStart]
}

func (p *StreamParser) CodePayload() string {
	full := p.fullBuffer.String()
	codeStart := strings.Index(full, p.markerCode)
	if codeStart == -1 {
		return ""
	}
	codeStart += len(p.markerCode)
	return full[codeStart:]
}

func (p *StreamParser) State() int { return 0 }

// Flush emits any remaining buffered content that the safety margin was holding.
// Must be called when the stream is fully done (EventTypeDone).
func (p *StreamParser) Flush() string {
	full := p.fullBuffer.String()
	// If there's an active VRAX tool block, don't flush chat
	if strings.Contains(full, p.markerTool) {
		return ""
	}

	chatStart := strings.Index(full, p.markerChat)
	var from int
	if chatStart != -1 {
		from = chatStart + len(p.markerChat)
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
		if idx := strings.Index(remaining, "<<<"); idx != -1 {
			remaining = remaining[:idx]
		}
		return remaining
	}
	return ""
}
