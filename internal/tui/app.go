package tui

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/patagonicrune/vraxter/internal/llm"
)

const vraxterIconPlaceholder = "[[VRAXTER_IDENTITY]]"

var (
	// Pallet
	colorOnyx      = lipgloss.Color("#0A0A0A")
	colorDeepGray  = lipgloss.Color("#161616")
	colorGold      = lipgloss.Color("#FFD700")
	colorOrange    = lipgloss.Color("#FFAA00")
	colorEmerald   = lipgloss.Color("#00FF41")
	colorMutedGray = lipgloss.Color("#666666")
	colorUser      = lipgloss.Color("#88C0D0") // Nord bright blue for user

	appStyle = lipgloss.NewStyle().
			Background(colorOnyx)

	headerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorGold).
			Padding(1, 2)

	tipsBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorOrange).
			Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Foreground(colorGold).
			Bold(true)

	userStyle = lipgloss.NewStyle().
			Foreground(colorOrange).
			Bold(true)

	agentStyle = lipgloss.NewStyle().
			Foreground(colorEmerald).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF1744")). // Vibrant Red
			Bold(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(colorGold).
			Italic(true)

	faintStyle = lipgloss.NewStyle().
			Foreground(colorMutedGray)

	statusBarStyle = lipgloss.NewStyle().
			Background(colorDeepGray).
			Foreground(colorGold).
			Bold(true).
			Padding(0, 1)

	thoughtStyle = lipgloss.NewStyle().
			Foreground(colorMutedGray).
			Italic(true)

	inputShelfStyle = lipgloss.NewStyle() // Transparent

	promptStyle = lipgloss.NewStyle().
			Foreground(colorGold).
			Bold(true).
			Padding(0, 1)

	sigilSpinner = spinner.Spinner{
		Frames: []string{
			` /`,
			` /\ `,
			` /\
  / `,
			` /\
 \/ `,
			`\/\/
 \/ `,
			`\/\/
/\/\`,
		},
		FPS: time.Millisecond * 300,
	}
)

type EventMsgWrapper struct {
	Event llm.StreamEvent
	Next  func() tea.Msg
}

type InfoMsg struct {
	ModelID string
	Status  string
}

type BlockType int

const (
	BlockMarkdown BlockType = iota
	BlockComponent
	BlockThought
)

type HistoryBlock struct {
	Type    BlockType
	Content string
}

type ClearStatusMsg struct{}

// HistoryResultMsg carries fetched conversation list back to the Update loop
type HistoryResultMsg struct {
	Conversations []client.ConversationSummary
	Err           error
}

type Model struct {
	SessionID            string
	History              []HistoryBlock
	Viewport             viewport.Model
	Input                textarea.Model
	Ready                bool
	IsStreaming          bool
	CurrentFocus         string // "vraxter" or "specialist"
	HasSentVraxterBanner bool   // Track if we've sent the banner for the current response
	Error                error
	ActiveModel          string
	OverrideModelID      string // Explicit override requested by user
	BaseModelID          string // Default from server
	grpcClient           *client.GRPCClient
	ctx                  context.Context
	ActiveSpecialistID   string // The specialist currently 'latched' to the session
	SpecialistBanner     string // The display name for the active specialist banner

	// Rendering
	renderer *glamour.TermRenderer

	// Feedback & Selection
	MouseSelectionMode bool
	StatusMessage      string
	viewportY          int // Dynamic top-offset for hit detection

	// Planning
	IsWaitingForApproval bool
	ProposedPlan         string // JSON content of the proposed plan
	PlanActionIndex      int    // 0:Proceed, 1:Reject, 2:Amend
	ShowAmendInput       bool

	// History browsing
	lastHistoryResults []client.ConversationSummary

	// UI State
	width    int
	height   int
	ShowHelp bool
	Spinner  spinner.Model
	UserName string

	// Chain of Thought
	isThinking bool
}

func NewModel(ctx context.Context, gClient *client.GRPCClient, session, initialQuery string, userName string) *Model {
	ta := textarea.New()
	ta.Placeholder = "Type a command for Vraxter..."
	ta.Prompt = "❯ "
	ta.Focus()
	ta.SetWidth(60)
	ta.SetHeight(1) // Start at one row

	// Match background exactly to the TUI (Ensure no black blocks)
	ta.FocusedStyle.Base = lipgloss.NewStyle().Background(nil)
	ta.BlurredStyle.Base = lipgloss.NewStyle().Background(nil)

	// Styled prompt: Bold Gold
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorGold).Bold(true)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(colorMutedGray)

	// Ensure cursor and styling is clean
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(colorGold)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(nil)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colorMutedGray)
	ta.ShowLineNumbers = false
	ta.MaxHeight = 5

	// Disable textarea's built-in enter-to-newline so we control submit vs newline
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "new line"))

	s := spinner.New()
	s.Spinner = sigilSpinner
	s.Style = lipgloss.NewStyle().Foreground(colorGold).Bold(true)

	// Initialize Markdown Renderer with a custom "Deep Onyx" theme
	charcoal := "#1A1A1A"
	empty := ""
	style := styles.DarkStyleConfig
	style.Document.BackgroundColor = &empty
	style.CodeBlock.BackgroundColor = &charcoal
	style.CodeBlock.Margin = nil
	style.Code.BackgroundColor = &charcoal
	style.Table.BackgroundColor = &charcoal

	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(80),
	)

	m := &Model{
		Input:        ta,
		History:      []HistoryBlock{},
		SessionID:    session,
		Ready:        false,
		ctx:          ctx,
		grpcClient:   gClient,
		Spinner:      s,
		ActiveModel:  "Detecting LLM...",
		CurrentFocus: "vraxter",
		renderer:     r,
		UserName:     strings.ToUpper(userName),
	}

	if initialQuery != "" {
		m.appendHistory(BlockComponent, m.renderBanner(m.UserName, "👤", false))
		m.appendHistory(BlockMarkdown, "  "+initialQuery+"\n\n")
		m.appendHistory(BlockMarkdown, vraxterIconPlaceholder+"  ")
		m.IsStreaming = true
	}

	return m
}

func (m *Model) fetchInfo() tea.Cmd {
	return func() tea.Msg {
		info, err := m.grpcClient.GetInfo(m.ctx)
		if err != nil {
			return InfoMsg{ModelID: "Offline", Status: "ERR"}
		}
		return InfoMsg{ModelID: info["model_id"], Status: info["status"]}
	}
}

func (m *Model) Init() tea.Cmd {
	if m.IsStreaming {
		return tea.Batch(
			textarea.Blink,
			m.Spinner.Tick,
			m.fetchInfo(),
			m.fireExecution(m.Input.Value()),
		)
	}
	return tea.Batch(
		textarea.Blink,
		m.Spinner.Tick,
		m.fetchInfo(),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var inputCmd, vpCmd tea.Cmd

	switch msg := msg.(type) {
	case ClearStatusMsg:
		m.StatusMessage = ""
		return m, nil

	case tea.KeyMsg:
		// 0. Immediate priority: Help Toggle
		if msg.String() == "?" && m.Input.Value() == "" {
			m.ShowHelp = !m.ShowHelp
			return m, nil
		}

		// 1. Intercept Global Controls
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		if m.IsWaitingForApproval {
			if msg.String() == "left" {
				m.PlanActionIndex = (m.PlanActionIndex - 1 + 3) % 3
				if m.PlanActionIndex != 2 {
					m.ShowAmendInput = false
					m.Input.Blur()
				}
				return m, nil
			}
			if msg.String() == "right" {
				m.PlanActionIndex = (m.PlanActionIndex + 1) % 3
				if m.PlanActionIndex != 2 {
					m.ShowAmendInput = false
					m.Input.Blur()
				}
				return m, nil
			}
			if msg.String() == "esc" {
				cmd := m.handleSlashCommand("/reject")
				if m.Ready {
					m.Viewport.SetContent(m.renderMarkdown(m.History))
					m.Viewport.GotoBottom()
				}
				return m, cmd
			}
		}

		// 2. Intercept Specific Key Behaviors
		// Shift+Enter: most terminals send Ctrl+J (Line Feed) for Shift+Enter.
		// Alt+Enter: universally distinguishable across all terminals.
		// Both insert a newline and grow the textarea.
		isNewlineShortcut := (msg.Alt && msg.Type == tea.KeyEnter) || msg.Type == tea.KeyCtrlJ
		if isNewlineShortcut {
			if !m.IsStreaming {
				// Manually insert newline and grow visible height
				m.Input.InsertString("\n")
				lines := m.Input.LineCount()
				if lines > 5 {
					lines = 5
				}
				if lines < 1 {
					lines = 1
				}
				m.Input.SetHeight(lines)
				return m, nil
			}
			return m, nil
		}

		// Viewport navigation: pgup/pgdn always work, up/down when input is empty
		switch msg.String() {
		case "pgup", "pgdown":
			m.Viewport, vpCmd = m.Viewport.Update(msg)
			return m, vpCmd
		case "home":
			m.Viewport.GotoTop()
			return m, nil
		case "end":
			m.Viewport.GotoBottom()
			return m, nil
		case "up", "down":
			if m.Input.Value() == "" {
				m.Viewport, vpCmd = m.Viewport.Update(msg)
				return m, vpCmd
			}
		}

		// 3. Handle Submissions (Plain Enter only)
		isPlainEnter := msg.String() == "enter" && !msg.Alt &&
			!strings.Contains(msg.String(), "ctrl+") &&
			!strings.Contains(msg.String(), "shift+")

		if isPlainEnter && !m.IsStreaming {
			if m.IsWaitingForApproval && !m.ShowAmendInput {
				switch m.PlanActionIndex {
				case 0: // PROCEED
					cmd := m.handleSlashCommand("/accept")
					if m.Ready {
						m.Viewport.SetContent(m.renderMarkdown(m.History))
						m.Viewport.GotoBottom()
					}
					return m, cmd
				case 1: // REJECT
					cmd := m.handleSlashCommand("/reject")
					if m.Ready {
						m.Viewport.SetContent(m.renderMarkdown(m.History))
						m.Viewport.GotoBottom()
					}
					return m, cmd
				case 2: // AMEND
					m.ShowAmendInput = true
					m.Input.Focus()
					return m, nil
				}
			}

			val := m.Input.Value()
			if strings.TrimSpace(val) == "" && !m.IsWaitingForApproval {
				return m, nil
			}

			// Clean message and reset
			messageToSend := strings.TrimSpace(val)
			m.Input.SetValue("")
			m.Input.SetHeight(1)

			// Timeline Refactor: User Identity Banner
			m.appendHistory(BlockComponent, m.renderBanner(m.UserName, "👤", false))
			m.appendHistory(BlockMarkdown, "  "+messageToSend+"\n")

			if strings.HasPrefix(messageToSend, "/") {
				cmd := m.handleSlashCommand(messageToSend)
				if m.Ready {
					m.Viewport.SetContent(m.renderMarkdown(m.History))
					m.Viewport.GotoBottom()
				}
				return m, cmd
			}

			// Prepare for Vraxter response
			m.HasSentVraxterBanner = false
			if m.Ready {
				m.Viewport.SetContent(m.renderMarkdown(m.History))
				m.Viewport.GotoBottom()
			}
			m.IsStreaming = true
			m.Error = nil

			if m.IsWaitingForApproval {
				return m, m.fireExecution(fmt.Sprintf("AMEND PLAN: %s", messageToSend))
			}

			return m, m.fireExecution(messageToSend)
		}

		// 4. Default: Update Component (Ensures single update path)
		if !m.IsStreaming {
			m.Input, inputCmd = m.Input.Update(msg)
			return m, inputCmd
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// 1. Update Renderer Wrap Width FIRST
		charcoal := "#1A1A1A"
		empty := ""
		style := styles.DarkStyleConfig
		style.Document.BackgroundColor = &empty
		style.CodeBlock.BackgroundColor = &charcoal
		style.Code.BackgroundColor = &charcoal
		style.CodeBlock.Margin = nil
		style.Table.BackgroundColor = &charcoal

		newRenderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(style),
			glamour.WithWordWrap(msg.Width-8),
		)
		if err == nil {
			m.renderer = newRenderer
		}

		// 2. Robust Dynamic Height Calculation
		headerView := m.renderHeader()
		m.viewportY = lipgloss.Height(headerView)
		headerHeight := lipgloss.Height(headerView)
		verticalMarginHeight := headerHeight + 10

		if !m.Ready {
			m.Viewport = viewport.New(msg.Width-4, msg.Height-verticalMarginHeight)
			m.Viewport.Width = msg.Width - 4
			m.Ready = true
		}

		// 3. Update Content AFTER renderer is ready
		m.Viewport.SetContent(m.renderMarkdown(m.History))
		m.Input.SetWidth(msg.Width - 6)

	case tea.MouseMsg:
		// 1. Always ignore motion/drag events to allow native terminal selection
		if msg.Type == tea.MouseMotion {
			return m, nil
		}

		// 2. Perform hit-detection for interaction
		// We check if the mouse is within the viewport's vertical bounds
		inBounds := msg.Y >= m.viewportY && msg.Y < m.viewportY+m.Viewport.Height

		if inBounds {
			// Capture scroll wheel events and clicks
			if msg.Type == tea.MouseWheelUp || msg.Type == tea.MouseWheelDown || msg.Type == tea.MouseLeft {
				m.Viewport, vpCmd = m.Viewport.Update(msg)
				return m, vpCmd
			}
		}

		// Button Hit Detection
		if m.IsWaitingForApproval && msg.Type == tea.MouseLeft {
			// Footer buttons are typically in the last 10 rows
			// We iterate through button X/Y bounds
			// Approximated for now based on renderDecisionBridge
			footerHeight := 5
			if m.ShowAmendInput {
				footerHeight = 8
			}
			buttonRow := m.height - footerHeight - 1
			if msg.Y >= buttonRow && msg.Y <= buttonRow+2 {
				// 3 buttons across footerWidth
				btnWidth := (m.width - 4) / 3
				if msg.X >= 2 && msg.X < 2+btnWidth {
					m.PlanActionIndex = 0
					return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				} else if msg.X >= 2+btnWidth && msg.X < 2+(btnWidth*2) {
					m.PlanActionIndex = 1
					return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				} else if msg.X >= 2+(btnWidth*2) && msg.X < m.width-2 {
					m.PlanActionIndex = 2
					m.ShowAmendInput = true
					m.Input.Focus()
					return m, nil
				}
			}
		}

		return m, nil

	case InfoMsg:
		m.BaseModelID = msg.ModelID
		if m.OverrideModelID == "" {
			m.ActiveModel = m.BaseModelID
		} else {
			m.ActiveModel = fmt.Sprintf("%s (LOCKED)", m.OverrideModelID)
		}

	case HistoryResultMsg:
		if msg.Err != nil {
			m.appendHistory(BlockComponent, errorStyle.Render(fmt.Sprintf("❌ Failed to fetch history: %v\n\n", msg.Err)))
		} else if len(msg.Conversations) == 0 {
			m.appendHistory(BlockComponent, toolStyle.Render("📭 No conversation history found.\n\n"))
		} else {
			m.lastHistoryResults = msg.Conversations

			var sb strings.Builder
			sb.WriteString("\n📜 **Conversation History**\n\n")
			sb.WriteString("| # | Title | Messages | Last Active |\n")
			sb.WriteString("|---|-------|----------|-------------|\n")
			for i, c := range msg.Conversations {
				title := c.Title
				if len(title) > 40 {
					title = title[:40] + "…"
				}
				updated := c.UpdatedAt
				if t, err := time.Parse(time.RFC3339, c.UpdatedAt); err == nil {
					updated = t.Format("Jan 02 15:04")
				}
				sb.WriteString(fmt.Sprintf("| %d | %s | %d | %s |\n", i+1, title, c.MessageCount, updated))
			}
			sb.WriteString("\n💡 Type `/load <#>` to replay a conversation (e.g. `/load 1`).\n")

			m.appendHistory(BlockMarkdown, sb.String())
		}
		if m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
			m.Viewport.GotoBottom()
		}
		return m, nil

	case EventMsgWrapper:
		// Real-time Model Sync: Update the header if the server's driving model changed
		if msg.Event.ActiveModelID != "" && msg.Event.ActiveModelID != m.ActiveModel {
			m.ActiveModel = msg.Event.ActiveModelID
		}

		switch msg.Event.Type {
		case llm.EventTypeToken:
			content := msg.Event.Content

			// Chain of Thought (CoT) Detection
			if !m.isThinking && strings.Contains(content, "<thought>") {
				m.isThinking = true
				parts := strings.SplitN(content, "<thought>", 2)
				if parts[0] != "" {
					m.appendHistory(BlockMarkdown, parts[0])
				}
				m.appendHistory(BlockThought, "> Thinking...\n")
				if parts[1] != "" {
					m.appendHistory(BlockThought, parts[1])
				}
				return m, nil
			}

			if m.isThinking && strings.Contains(content, "</thought>") {
				m.isThinking = false
				parts := strings.SplitN(content, "</thought>", 2)
				if parts[0] != "" {
					m.appendHistory(BlockThought, parts[0])
				}
				if parts[1] != "" {
					// Re-ensure banner if CoT was the first thing sent
					if !m.HasSentVraxterBanner {
						m.appendHistory(BlockComponent, m.renderBanner("VRAXTER", "◈", true))
						m.HasSentVraxterBanner = true
					}
					m.appendHistory(BlockMarkdown, parts[1])
				}
				return m, nil
			}

			if !m.HasSentVraxterBanner && !m.isThinking {
				bannerLabel := "VRAXTER"
				if m.CurrentFocus == "specialist" && m.SpecialistBanner != "" {
					bannerLabel = m.SpecialistBanner
				}
				m.appendHistory(BlockComponent, m.renderBanner(bannerLabel, "◈", true))
				m.HasSentVraxterBanner = true
			}

			if m.isThinking {
				m.appendHistory(BlockThought, content)
			} else {
				m.appendHistory(BlockMarkdown, content)
			}
		case llm.EventTypeStatus:
			m.StatusMessage = msg.Event.Content
			cmds = append(cmds, func() tea.Msg {
				time.Sleep(4 * time.Second)
				return ClearStatusMsg{}
			})
		case llm.EventTypeSkillCall:
			label := msg.Event.Content
			icon := "⚙️"
			if label == "vraxter-return-control" {
				label = "Specialist boundary reached. Resuming Supervisor context..."
				icon = "◈"
				m.CurrentFocus = "vraxter"     // ◈ Focus shifts for THIS turn
				m.HasSentVraxterBanner = false // Allow supervisor banner to spawn
				m.ActiveSpecialistID = ""      // Clear latched specialist
				m.SpecialistBanner = ""
			} else {
				label = fmt.Sprintf("Running tool: [%s]...", label)
			}
			toolNotice := lipgloss.NewStyle().
				Foreground(colorGold).
				Italic(true).
				Bold(true).
				Render(fmt.Sprintf("  %s  %s", icon, label))
			m.appendHistory(BlockComponent, toolNotice+"\n\n")
		case llm.EventTypePlanProposal:
			m.IsWaitingForApproval = true
			m.ProposedPlan = msg.Event.Content
			m.PlanActionIndex = 0 // Default to PROCEED
			m.ShowAmendInput = false
			m.Input.Blur()
			m.appendHistory(BlockComponent, m.renderPlan(msg.Event.Content, m.width, "")+"\n")
		case llm.EventTypeSpecialistResult:
			// Format: id|name|content
			parts := strings.SplitN(msg.Event.Content, "|", 3)
			if len(parts) >= 2 {
				id := parts[0]
				name := parts[1]
				actualContent := ""
				if len(parts) == 3 {
					actualContent = parts[2]
				}

				// Latch the Context & Focus
				m.ActiveSpecialistID = id
				m.SpecialistBanner = strings.ToUpper(name)
				m.CurrentFocus = "specialist"

				// Prepend the Rich Specialist Banner if not sent yet
				if !m.HasSentVraxterBanner {
					m.appendHistory(BlockComponent, m.renderBanner(m.SpecialistBanner, "◈", true))
					m.HasSentVraxterBanner = true
				}

				if actualContent != "" {
					m.appendHistory(BlockMarkdown, actualContent)
				}
			}
		case llm.EventTypeError:
			m.Error = msg.Event.Err
			m.IsStreaming = false
			errNotice := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF1744")).
				Bold(true).
				Render(fmt.Sprintf("  ❌  Error: %v", m.Error))
			m.appendHistory(BlockComponent, errNotice+"\n\n")
		case llm.EventTypeDone:
			m.IsStreaming = false
			m.appendHistory(BlockMarkdown, "\n\n")
		}

		if m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
			// Only lock to bottom on active execution streams
			m.Viewport.GotoBottom()
		}

		if m.IsStreaming && msg.Next != nil {
			return m, tea.Batch(append(cmds, msg.Next)...)
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		// If we are in the "Thinking" state, we need to refresh the chat to show the animation
		if m.IsStreaming && !m.HasSentVraxterBanner && m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
		}
		return m, cmd
	}

	if !m.IsStreaming {
		m.Input, inputCmd = m.Input.Update(msg)
		cmds = append(cmds, inputCmd)
	}

	// We pass unspecified keys downwards to scrolling (like mousewheels, etc.)
	// ONLY if selection mode is OFF
	if !m.MouseSelectionMode {
		m.Viewport, vpCmd = m.Viewport.Update(msg)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) appendHistory(t BlockType, content string) {
	if len(m.History) > 0 && m.History[len(m.History)-1].Type == t && (t == BlockMarkdown || t == BlockThought) {
		m.History[len(m.History)-1].Content += content
	} else {
		m.History = append(m.History, HistoryBlock{Type: t, Content: content})
	}
}

func (m *Model) handleSlashCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "/clear":
		m.History = []HistoryBlock{}
		return nil
	case "/session":
		if len(parts) > 1 {
			m.SessionID = parts[1]
			m.appendHistory(BlockComponent, fmt.Sprintf("**🔄 Session switched to '%s'**\n\n", m.SessionID))
		} else {
			m.appendHistory(BlockComponent, "**❌ Usage: /session <id>**\n\n")
		}
		return nil
	case "/help", "/?":
		help := `
🤖 Vraxter TUI Commands:
  /activate <id>    - Manually switch and lock the active model for this session
  /restore, /auto   - Restore automatic use-case based model routing
  /plan <task>      - Break down a complex task into a multi-phase plan
  /accept           - Approve and execute the current proposed plan
  /reject           - Cancel the current proposed plan
  /history          - Browse your past conversation sessions
  /load <#>         - Load a past conversation by its row number from /history
  /clear            - Clears the chat history from the screen
  /new, /reset      - Generates a fresh session ID and clears history
  /session <id>     - Switches your context to a different session ID
  /models [args]    - Proxy: Run 'vraxter models' directly within TUI
  /skills [args]    - Proxy: Run 'vraxter skills' directly within TUI
  /quit             - Exits Vraxter (or press Ctrl+C)
  /help, /?         - Shows this help menu
`
		m.appendHistory(BlockComponent, toolStyle.Render(help)+"\n\n")
		return nil
	case "/activate":
		if len(parts) < 2 {
			m.appendHistory(BlockComponent, errorStyle.Render("❌ Usage: /activate <model_id_or_alias>\n\n"))
			return nil
		}
		m.OverrideModelID = parts[1]
		m.ActiveModel = fmt.Sprintf("%s (LOCKED)", m.OverrideModelID)
		m.appendHistory(BlockComponent, toolStyle.Render(fmt.Sprintf("🎯 Model override active: %s. All future requests will ignore automatic routing.\n\n", m.OverrideModelID)))
		return nil
	case "/restore", "/auto":
		m.OverrideModelID = ""
		m.appendHistory(BlockComponent, toolStyle.Render("🔄 Model routing restored to automatic (Use-Case driven).\n\n"))
		return m.fetchInfo()
	case "/history":
		return func() tea.Msg {
			convs, err := m.grpcClient.ListHistory(m.ctx, 20)
			return HistoryResultMsg{Conversations: convs, Err: err}
		}
	case "/load":
		if len(parts) < 2 {
			m.appendHistory(BlockComponent, errorStyle.Render("❌ Usage: /load <#> — Use the row number from /history\n\n"))
			return nil
		}

		// Try parsing as a numeric index first
		var targetID string
		if idx, err := strconv.Atoi(parts[1]); err == nil {
			if idx < 1 || idx > len(m.lastHistoryResults) {
				if len(m.lastHistoryResults) == 0 {
					m.appendHistory(BlockComponent, errorStyle.Render("❌ Run /history first to see available conversations.\n\n"))
				} else {
					m.appendHistory(BlockComponent, errorStyle.Render(fmt.Sprintf("❌ Invalid index. Choose a number between 1 and %d.\n\n", len(m.lastHistoryResults))))
				}
				return nil
			}
			targetID = m.lastHistoryResults[idx-1].ID
		} else {
			// Fallback: accept raw session ID for power users
			targetID = parts[1]
		}

		displayLabel := targetID
		if len(displayLabel) > 30 {
			displayLabel = displayLabel[:30] + "…"
		}
		m.appendHistory(BlockComponent, toolStyle.Render(fmt.Sprintf("📂 Loading conversation '%s'...\n\n", displayLabel)))

		return func() tea.Msg {
			msgs, err := m.grpcClient.GetConversation(m.ctx, targetID)
			if err != nil {
				return EventMsgWrapper{Event: llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n❌ Failed to load conversation: %v\n", err)}}
			}
			if len(msgs) == 0 {
				return EventMsgWrapper{Event: llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n📭 No messages found for this session.\n"}}
			}

			// Build the replay content as a single markdown block
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("\n📜 **Replaying session** (%d messages)\n\n---\n\n", len(msgs)))
			for _, msg := range msgs {
				ts := msg.Timestamp
				if t, err := time.Parse(time.RFC3339, msg.Timestamp); err == nil {
					ts = t.Format("Jan 02 15:04")
				}
				switch msg.Role {
				case "user":
					sb.WriteString(fmt.Sprintf("**👤 You** _%s_\n\n%s\n\n---\n\n", ts, msg.Content))
				case "assistant":
					sb.WriteString(fmt.Sprintf("**◈ Vraxter** _%s_\n\n%s\n\n---\n\n", ts, msg.Content))
				case "system":
					sb.WriteString(fmt.Sprintf("**⚙️ System** _%s_\n\n_%s_\n\n---\n\n", ts, msg.Content))
				}
			}

			return EventMsgWrapper{Event: llm.StreamEvent{Type: llm.EventTypeToken, Content: sb.String()}}
		}
	case "/plan":
		if len(parts) < 2 {
			m.appendHistory(BlockComponent, errorStyle.Render("❌ Usage: /plan <task details>\n\n"))
			return nil
		}
		m.appendHistory(BlockMarkdown, "  ") // Use simple indentation for standard chat markers to avoid height overload
		m.IsStreaming = true
		return m.fireExecution(cmd)
	case "/new", "/reset":
		m.SessionID = fmt.Sprintf("new-%d-%x", time.Now().Unix(), md5.Sum([]byte(uuid.New().String())))
		m.History = []HistoryBlock{}
		m.appendHistory(BlockComponent, toolStyle.Render("✨ Memory Refreshed. New Session Started.\n\n"))
		return nil
	case "/accept":
		if !m.IsWaitingForApproval {
			m.appendHistory(BlockComponent, errorStyle.Render("❌ No plan currently awaiting approval.\n\n"))
			return nil
		}

		// Update History in-place to show Acceptance INSIDE the box
		if len(m.History) > 0 {
			m.History[len(m.History)-1].Content = m.renderPlan(m.ProposedPlan, m.width, "✅ PLAN ACCEPTED") + "\n"
		}

		m.IsWaitingForApproval = false
		m.IsStreaming = true

		// Restore Input State
		m.Input.Prompt = "❯ "
		m.Input.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorGold).Bold(true)
		m.Input.Focus()

		return m.fireExecution("!EXECUTE_PLAN " + m.ProposedPlan)
	case "/reject":
		// Update History in-place to show Rejection INSIDE the box
		if len(m.History) > 0 {
			m.History[len(m.History)-1].Content = m.renderPlan(m.ProposedPlan, m.width, "❌ PLAN REJECTED") + "\n"
		}

		m.IsWaitingForApproval = false
		m.ProposedPlan = ""

		// Restore Input State
		m.Input.Prompt = "❯ "
		m.Input.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorGold).Bold(true)
		m.Input.Focus()

		return nil
	case "/quit", "/exit":
		m.appendHistory(BlockComponent, toolStyle.Render("👋 Press Ctrl+C or ESC to exit.\n\n"))
		return nil
	case "/models", "/skills", "/add", "/specialists":
		m.appendHistory(BlockComponent, fmt.Sprintf("\n\n⚙️ Running local CLI: %s\n", cmd))
		execArgs := append([]string{strings.TrimPrefix(parts[0], "/")}, parts[1:]...)

		return func() tea.Msg {
			goCmd := exec.Command(os.Args[0], execArgs...)
			out, _ := goCmd.CombinedOutput()

			// Filter out ghost input artifacts from sub-processes
			cleanOut := strings.ReplaceAll(string(out), "Type a command for Vraxter... (Ctrl+C to quit)", "")
			cleanOut = stripANSI(strings.TrimSpace(cleanOut))

			// Wrap in code block to preserve Tables and avoid glamour mangling
			return EventMsgWrapper{Event: llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n```\n" + cleanOut + "\n```\n"}}
		}
	default:
		m.appendHistory(BlockComponent, errorStyle.Render(fmt.Sprintf("❌ Unknown command: %s. Type /help for options.\n\n", parts[0])))
		return nil
	}
}

func (m *Model) renderPlan(jsonStr string, width int, status string) string {
	var plan struct {
		Narrative string `json:"narrative"`
		Goal      string `json:"goal"`
		Reasoning string `json:"reasoning"`
		Phases    []struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Tasks       []string `json:"tasks"`
			Specialist  string   `json:"specialist_id"`
		} `json:"phases"`
		Estimations string `json:"estimations"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return errorStyle.Render(fmt.Sprintf("❌ Error parsing plan: %v", err))
	}

	// Staff-grade Palette Harmonization
	accentStyle := lipgloss.NewStyle().Foreground(colorGold).Bold(true)
	phaseStyle := lipgloss.NewStyle().Foreground(colorEmerald).Bold(true)
	assigneeStyle := lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorMutedGray).Italic(true)
	separatorStyle := lipgloss.NewStyle().Foreground(colorMutedGray).Faint(true)

	boxWidth := width - 10
	if boxWidth < 50 {
		boxWidth = 50
	}

	doc := strings.Builder{}

	// 1. SECTION: Strategy (Narrative)
	if plan.Narrative != "" {
		// Clone and customize the style to match Vraxter Palette
		style := styles.DarkStyleConfig
		style.Document.BackgroundColor = nil // Transparent/Inherited

		// Inject colors (converting lipgloss.Color to string)
		gold := string(colorGold)
		emerald := string(colorEmerald)
		orange := string(colorOrange)
		empty := ""

		style.H1.Color = &gold
		style.H1.BackgroundColor = nil
		style.H1.Prefix = empty
		style.H1.Suffix = empty

		style.H2.Color = &gold
		style.H2.BackgroundColor = nil
		style.H2.Prefix = empty
		style.H2.Suffix = empty

		style.H3.Color = &gold
		style.H3.BackgroundColor = nil
		style.H3.Prefix = empty
		style.H3.Suffix = empty

		style.Item.Color = &emerald
		style.Strong.Color = &emerald
		style.Emph.Color = &orange
		style.Link.Color = &orange

		// USE LOCAL RENDERER TO AVOID STATE COLLISION
		r, _ := glamour.NewTermRenderer(
			glamour.WithStyles(style),
			glamour.WithWordWrap(boxWidth-6),
		)

		renderedNarrative, _ := r.Render(plan.Narrative)
		doc.WriteString(renderedNarrative)

		doc.WriteString("\n" + separatorStyle.Render(strings.Repeat("─", boxWidth-4)) + "\n\n")
	}

	// 2. SECTION: Execution (Manifest)
	doc.WriteString(accentStyle.Render("📋 IMPLEMENTATION MANIFEST") + "\n\n")

	goalWrap := lipgloss.NewStyle().Width(boxWidth - 4).Render(accentStyle.Render("🎯 Goal: ") + plan.Goal)
	doc.WriteString(goalWrap + "\n")

	reasoningWrap := lipgloss.NewStyle().Width(boxWidth - 4).Render(lipgloss.NewStyle().Italic(true).Faint(true).Render("🤔 Reasoning: ") + plan.Reasoning)
	doc.WriteString(reasoningWrap + "\n\n")

	for i, p := range plan.Phases {
		doc.WriteString(fmt.Sprintf("%s %d: %s\n", phaseStyle.Render("Phase"), i+1, p.Title))
		doc.WriteString(lipgloss.NewStyle().Width(boxWidth-6).Render(descStyle.Render("   "+p.Description)) + "\n")
		for _, t := range p.Tasks {
			doc.WriteString(fmt.Sprintf("   - %s\n", t))
		}
		doc.WriteString(fmt.Sprintf("   %s %s\n\n", assigneeStyle.Render("Assignee:"), p.Specialist))
	}

	doc.WriteString(accentStyle.Render("⏳ Estimate: ") + plan.Estimations)

	if status != "" {
		doc.WriteString("\n\n" + separatorStyle.Render(strings.Repeat("─", boxWidth-4)) + "\n")
		statusStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		if strings.Contains(status, "ACCEPTED") {
			statusStyle = statusStyle.Foreground(colorEmerald)
		} else {
			statusStyle = statusStyle.Foreground(lipgloss.Color("#FF5252"))
		}
		doc.WriteString(statusStyle.Render(status) + "\n")
	} else {
		doc.WriteString("\n\n")
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGold).
		Padding(1, 2).
		Width(boxWidth).
		Render(doc.String())
}

func (m *Model) renderDecisionBridge(width int) string {
	btnStyle := lipgloss.NewStyle().
		Bold(true).
		Padding(0, 3).
		MarginRight(2)

	// Define Ghost Button Styles
	proceedBase := btnStyle.Copy().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#1B5E20")).Foreground(lipgloss.Color("#1B5E20"))
	rejectBase := btnStyle.Copy().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#B71C1C")).Foreground(lipgloss.Color("#B71C1C"))
	amendBase := btnStyle.Copy().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#FF8F00")).Foreground(lipgloss.Color("#FF8F00"))

	// Highlight the active button with a solid fill
	if m.PlanActionIndex == 0 {
		proceedBase = proceedBase.Copy().Background(colorEmerald).Foreground(colorOnyx).BorderForeground(colorEmerald).Bold(true)
	} else if m.PlanActionIndex == 1 {
		rejectBase = rejectBase.Copy().Background(lipgloss.Color("#FF5252")).Foreground(colorOnyx).BorderForeground(lipgloss.Color("#FF5252")).Bold(true)
	} else if m.PlanActionIndex == 2 {
		amendBase = amendBase.Copy().Background(lipgloss.Color("#FFD54F")).Foreground(colorOnyx).BorderForeground(lipgloss.Color("#FFD54F")).Bold(true)
	}

	buttons := lipgloss.JoinHorizontal(lipgloss.Top,
		proceedBase.Render("PROCEED"),
		rejectBase.Render("REJECT"),
		amendBase.Render("AMEND"),
	)

	bridge := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(colorGold).
		Padding(1, 0).
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render(" ◈ DECISION REQUIRED"),
			"",
			buttons,
		))

	if m.ShowAmendInput {
		m.Input.Prompt = "❯ FEEDBACK: "
		m.Input.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
		inputArea := lipgloss.NewStyle().
			PaddingTop(1).
			Render(m.Input.View())

		bridge = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(colorGold).
			Padding(1, 0).
			Width(width).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render(" ◈ DECISION REQUIRED"),
				"",
				buttons,
				inputArea,
			))
	}

	return bridge
}

func (m Model) fireExecution(query string) tea.Cmd {
	return func() tea.Msg {
		stream, err := m.grpcClient.ExecuteStream(m.ctx, query, m.SessionID, m.ActiveSpecialistID, m.OverrideModelID)
		if err != nil {
			return EventMsgWrapper{Event: llm.StreamEvent{Type: llm.EventTypeError, Err: err}}
		}
		return readNextEvent(stream)
	}
}

func readNextEvent(stream <-chan llm.StreamEvent) tea.Msg {
	event, ok := <-stream
	if !ok {
		return EventMsgWrapper{Event: llm.StreamEvent{Type: llm.EventTypeDone}}
	}
	return EventMsgWrapper{Event: event, Next: func() tea.Msg { return readNextEvent(stream) }}
}

func (m *Model) View() string {
	if !m.Ready {
		return "Initializing Dashboard..."
	}

	header := m.renderHeader()

	chatArea := lipgloss.NewStyle().
		Padding(0, 1).
		Height(m.height - lipgloss.Height(header) - m.Input.Height() - 7). // Subtle Air Balance
		Render(m.Viewport.View())

	// --- COMMAND DECK (DOUBLE-RAIL CONSOLE) ---
	footerWidth := m.width - 4

	// 1. Top Rail w/ Integrated Shortcuts & Identity Pill
	shortcutPills := lipgloss.NewStyle().
		Foreground(colorGold).
		Bold(true).
		Render(" /? HELP • Shift+↵ NEWLINE • ^C QUIT ")

	if m.IsWaitingForApproval {
		shortcutPills = lipgloss.NewStyle().
			Foreground(colorGold).
			Bold(true).
			Render(" ←/→ NAVIGATE • ENTER CONFIRM • ESC REJECT ")
	}

	identityLabel := " FOCUS: SUPERVISOR FOCUS "
	if m.SpecialistBanner != "" {
		identityLabel = fmt.Sprintf(" FOCUS: %s ", m.SpecialistBanner)
	}
	identityPill := lipgloss.NewStyle().
		Background(colorDeepGray).
		Foreground(colorOrange).
		Bold(true).
		Render(identityLabel)

	// Build the line with the shortcuts embedded at the start and identity at the end
	railTotalWidth := footerWidth - 2
	lineBg := strings.Repeat("─", railTotalWidth-lipgloss.Width(shortcutPills)-lipgloss.Width(identityPill)-2)
	topLine := faintStyle.Render("──") + shortcutPills + faintStyle.Render("─"+lineBg+"─") + identityPill + faintStyle.Render("──")

	// 2. Input Area
	var inputShelf string
	if m.IsWaitingForApproval {
		inputShelf = m.renderDecisionBridge(footerWidth)
	} else {
		inputContent := m.Input.View()
		if m.IsStreaming {
			inputContent = faintStyle.Render("❯ Vraxter is processing...")
		}

		inputShelf = inputShelfStyle.
			Width(footerWidth).
			Render(inputContent)
	}

	// 3. Bottom Rail
	bottomRail := faintStyle.Render(strings.Repeat("─", footerWidth))

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		chatArea,
		topLine,
		"", // Subtle Air Spacer
		inputShelf,
		bottomRail,
	)
}

func (m *Model) renderMarkdown(history []HistoryBlock) string {
	var fullContent strings.Builder

	for i, block := range history {
		if block.Type == BlockMarkdown {
			content := block.Content
			// Identity Alignment: Indent responses that follow an agent banner
			bodyStyle := lipgloss.NewStyle()
			if i > 0 && history[i-1].Type == BlockComponent && strings.Contains(history[i-1].Content, "◈") {
				bodyStyle = bodyStyle.PaddingLeft(2)
			}

			// Hard Strip for safety before rendering if it accidentally got ANSI (shouldn't happen for BlockMarkdown)
			content = stripANSI(content)

			// --- PRO-CODE SPLITTER LOGIC ---
			re := regexp.MustCompile("(?s)```(\\w+)?\n(.*?)\n```")
			lastIndex := 0
			matches := re.FindAllStringSubmatchIndex(content, -1)

			for _, match := range matches {
				// 1. Render text before the code block
				if match[0] > lastIndex {
					textSegment := content[lastIndex:match[0]]
					out, err := m.renderer.Render(textSegment)
					if err == nil {
						fullContent.WriteString(bodyStyle.Render(out))
					} else {
						fullContent.WriteString(bodyStyle.Render(textSegment))
					}
				}

				// 2. Render the Code Block as a specialized Component
				lang := ""
				if match[2] != -1 && match[3] != -1 {
					lang = content[match[2]:match[3]]
				}
				code := content[match[4]:match[5]]

				fullContent.WriteString(m.renderCodeBlock(lang, code, m.width-8)) // Width minus safety gutter
				fullContent.WriteString("\n")

				lastIndex = match[1]
			}

			// 3. Render remaining text
			if lastIndex < len(content) {
				textSegment := content[lastIndex:]
				out, err := m.renderer.Render(textSegment)
				if err == nil {
					fullContent.WriteString(bodyStyle.Render(out))
				} else {
					fullContent.WriteString(bodyStyle.Render(textSegment))
				}
			}
		} else if block.Type == BlockThought {
			fullContent.WriteString(thoughtStyle.Render(block.Content) + "\n\n")
		} else {
			// Component block: render directly bypassing Glamour
			fullContent.WriteString(block.Content)
		}
	}

	// 3. Optional: Neural Link Thinking Indicator
	if m.IsStreaming && !m.HasSentVraxterBanner {
		// Vertically center the text relative to the 2-line spinner for stability
		indicatorText := "Working..."
		joined := lipgloss.JoinHorizontal(lipgloss.Center,
			m.Spinner.View(),
			lipgloss.NewStyle().PaddingLeft(2).Render(indicatorText),
		)

		thinkingIndicator := lipgloss.NewStyle().
			Foreground(colorGold).
			Faint(true).
			Padding(0, 2).
			Render(joined)

		fullContent.WriteString("\n" + thinkingIndicator + "\n")
	}

	return fullContent.String()
}

func (m *Model) renderCodeBlock(lang, code string, width int) string {
	charcoal := "#1A1A1A" // Deep Charcoal (Classic Vraxter)
	bgCharcoal := lipgloss.Color(charcoal)

	if lang == "" {
		lang = "CODE"
	}
	lang = strings.ToUpper(lang)

	var inner string
	// Raw Fidelity Bypass: Skip Glamour for generic output to preserve alignment
	if lang == "CODE" {
		inner = strings.TrimSpace(code)
	} else {
		// Syntax Highlighting Path
		style := styles.DarkStyleConfig
		empty := ""
		style.Document.BackgroundColor = &empty
		style.CodeBlock.BackgroundColor = &empty
		style.Code.BackgroundColor = &empty
		style.CodeBlock.Margin = nil

		r, _ := glamour.NewTermRenderer(
			glamour.WithStyles(style),
			glamour.WithWordWrap(width-4),
		)
		rendered, _ := r.Render("```" + strings.ToLower(lang) + "\n" + code + "\n```")
		inner = strings.TrimSpace(rendered)
	}

	headerStyle := lipgloss.NewStyle().
		PaddingLeft(2). // MOVE THE MARGIN HERE
		Foreground(colorGold).
		Background(bgCharcoal).
		PaddingRight(1). // Consistent padding
		Bold(true)

	// Sovereign Substrate Style: Indent and Background fusion
	containerStyle := lipgloss.NewStyle().
		PaddingLeft(2). // Identity Indent
		Width(width).   // Solid Block Width
		Background(bgCharcoal)

	header := headerStyle.Render("◈ " + lang)

	return lipgloss.JoinVertical(lipgloss.Left,
		header, // Margin is now integrated into the style
		containerStyle.Render(inner),
	)
}

func (m *Model) renderBanner(label string, icon string, isVraxter bool) string {
	bannerWidth := m.width - 4
	if bannerWidth < 40 {
		bannerWidth = 40
	}

	style := lipgloss.NewStyle().
		Bold(true).
		Padding(0, 1).
		Width(bannerWidth)

	var labelPart string
	if isVraxter {
		style = style.Background(colorDeepGray).Foreground(colorGold).Align(lipgloss.Left)
		labelPart = fmt.Sprintf("%s %s", icon, label)
	} else {
		style = style.Background(colorDeepGray).Foreground(colorUser).Align(lipgloss.Left)
		labelPart = fmt.Sprintf("%s %s", icon, label)
	}

	return "\n" + style.Render(labelPart) + "\n"
}

func stripANSI(str string) string {
	const ansi = "[\u001B\u009B][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]"
	var re = regexp.MustCompile(ansi)
	return re.ReplaceAllString(str, "")
}

func (m *Model) renderHeader() string {
	mascot := m.renderMascot()

	statusText := agentStyle.Render("● System Ready | Multi-Model Active")
	if m.IsStreaming {
		statusText = lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render("● Vraxter is Thinking...")
	}

	// Left Side: Identity
	// Format to Title Case for the welcome message
	displayTitle := strings.ToLower(m.UserName)
	if len(displayTitle) > 0 {
		displayTitle = strings.ToUpper(string(displayTitle[0])) + displayTitle[1:]
	}
	if displayTitle == "You" || displayTitle == "" {
		displayTitle = "Guest"
	}
	infoPanel := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(fmt.Sprintf("Welcome back, %s!", displayTitle)),
		faintStyle.Render(fmt.Sprintf("Session: %s", m.SessionID)),
		statusText,
	)

	mascotHeight := lipgloss.Height(mascot)
	centeredInfo := lipgloss.NewStyle().
		Height(mascotHeight).
		AlignVertical(lipgloss.Center).
		Render(infoPanel)

	leftSegment := lipgloss.JoinHorizontal(lipgloss.Top,
		mascot,
		lipgloss.NewStyle().Width(3).Render(""),
		centeredInfo,
	)

	// Right Side: Tips & Model
	rightSide := []string{
		lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render("Tips for getting started"),
		faintStyle.Render("• /plan <task> for autonomous mode"),
		faintStyle.Render("• Use Specialists for expert tasks"),
		"",
	}
	if m.BaseModelID != "" && m.BaseModelID != m.ActiveModel {
		rightSide = append(rightSide, lipgloss.NewStyle().Foreground(colorEmerald).Faint(true).Render(fmt.Sprintf("Default: %s", m.BaseModelID)))
	}
	rightSide = append(rightSide, lipgloss.NewStyle().Foreground(colorEmerald).Bold(true).Render(fmt.Sprintf("Active:  %s", m.ActiveModel)))

	tips := lipgloss.JoinVertical(lipgloss.Left, rightSide...)

	// Combine both segments with a vertical separator
	separator := lipgloss.NewStyle().
		MarginLeft(1).
		MarginRight(1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorOrange).
		Height(8). // Match mascot height
		Render("")

	// Symmetrical Calculation: 50/50 split
	// m.width-4 is total box width. Deduct 2 for border and 4 for padding (Padding(1,2))
	availableWidth := m.width - 4 - 6
	halfWidth := availableWidth / 2

	leftCol := lipgloss.NewStyle().Width(halfWidth).Align(lipgloss.Left).Render(leftSegment)
	rightCol := lipgloss.NewStyle().Width(halfWidth).Align(lipgloss.Left).Render(tips)

	headerContent := lipgloss.JoinHorizontal(lipgloss.Top,
		leftCol,
		separator,
		rightCol,
	)

	// Anchored left within a correctly-sized gold box
	return headerBoxStyle.
		Width(m.width - 2).
		Render(headerContent)
}

func (m *Model) renderMascot() string {
	// Mascot: "The Crimson Sigil" (Full-Size Substrate)
	outerSigilStyle := lipgloss.NewStyle().Foreground(colorOrange)

	// Complex layered ASCII with colors - Interlocking Design
	mPart1 := outerSigilStyle.Render(`\    /\    /`)
	mPart2 := outerSigilStyle.Render(` \  /  \  / `)
	mPart3 := outerSigilStyle.Render(`  \/    \/  `)
	mPart4 := outerSigilStyle.Render(`  /\    /\  `)
	mPart5 := outerSigilStyle.Render(` /  \  /  \ `)
	mPart6 := outerSigilStyle.Render(`/    \/    \`)

	return lipgloss.JoinVertical(lipgloss.Center,
		mPart1, mPart2, mPart3, mPart4, mPart5, mPart6,
	)
}
