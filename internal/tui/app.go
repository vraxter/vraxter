package tui

import (
	"bytes"
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

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/utils"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
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
	colorUser      = lipgloss.Color("#B0BEC5")

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

	// --- Block Message Styles (Mockup Mode) ---
	// Backgrounds are derived as ~20% opacity of the sidebar color on black
	userBlockStyle = lipgloss.NewStyle().
			Border(lipgloss.Border{Left: "┃"}, false, false, false, true).
			BorderForeground(colorUser).
			Background(lipgloss.Color("#111315")).
			PaddingLeft(1).
			PaddingRight(1).
			PaddingTop(1).
			PaddingBottom(1).
			MarginBottom(1)

	vraxterBlockStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "┃"}, false, false, false, true).
				BorderForeground(colorGold).
				Background(lipgloss.Color("#191500")).
				PaddingLeft(1).
				PaddingRight(1).
				PaddingTop(1).
				PaddingBottom(1).
				MarginBottom(1)

	specialistBlockStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "┃"}, false, false, false, true).
				BorderForeground(colorOrange).
				Background(lipgloss.Color("#191100")).
				PaddingLeft(1).
				PaddingRight(1).
				PaddingTop(1).
				PaddingBottom(1).
				MarginBottom(1)

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

	ansibg      = regexp.MustCompile(`\x1b\[(4[0-9]|10[0-9]|48;[0-9];[0-9;]+)m`)
	codeBlockRe = regexp.MustCompile("(?s)```(\\w+)?\n(.*?)\n```")
)

var helpCommand = `
  /activate <id>		- Manually switch and lock the active model for this session
  /restore, /auto		- Restore automatic use-case based model routing
  /plan <task>		- Break down a complex task into a multi-phase plan
  /history		- Browse your past conversation sessions
  /load <#>		- Load a past conversation by its row number from /history
  /clear		- Clears the chat history from the screen
  /new, /reset		- Generates a fresh session ID and clears history
  /session <id>		- Switches your context to a different session ID
  /providers [args]	- Manage AI providers (/providers setup for wizard)
  /models [args]		- Manage models linked to providers
  /skills [args]		- Proxy: Run 'vraxter skills'
  /specialists [args]		- Proxy: Manage specialists
  /user [args]		- Proxy: Manage profile
  /add [args]		- Proxy: Quickly add skills
  /start		- Unified startup wizard (Profile ➔ Providers ➔ Models)
  /quit, /exit		- Exits Vraxter
  /help, /?		- Shows this menu
`


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

type SenderType string

const (
	SenderUser       SenderType = "user"
	SenderVraxter    SenderType = "vraxter"
	SenderSpecialist SenderType = "specialist"
	SenderSystem     SenderType = "system"
)

type HistoryBlock struct {
	Type    BlockType
	Content string
	Sender  SenderType
}

type ClearStatusMsg struct{}

// HistoryResultMsg carries fetched conversation list back to the Update loop
type HistoryResultMsg struct {
	Conversations []client.ConversationSummary
	Err           error
}

type cmdOutputMsg struct {
	input  string
	output string
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

	appStore    *db.Store
	appCrypto   *security.CryptoService
	ActiveSetup tea.Model
}

func NewModel(ctx context.Context, gClient *client.GRPCClient, store *db.Store, crypto *security.CryptoService, session, initialQuery string, userName string) *Model {
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
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "new line"))

	s := spinner.New()
	s.Spinner = sigilSpinner
	s.Style = lipgloss.NewStyle().Foreground(colorGold).Bold(true)

	r, _ := buildRenderer(80)

	m := &Model{
		Input:        ta,
		History:      []HistoryBlock{},
		SessionID:    session,
		Ready:        false,
		ctx:          ctx,
		grpcClient:   gClient,
		appStore:     store,
		appCrypto:    crypto,
		Spinner:      s,
		ActiveModel:  "Detecting LLM...",
		CurrentFocus: "vraxter",
		renderer:     r,
		UserName:     strings.ToUpper(userName),
	}

	if initialQuery != "" {
		m.appendHistory(BlockMarkdown, initialQuery, SenderUser)
		m.appendHistory(BlockMarkdown, vraxterIconPlaceholder+"  ", SenderVraxter)
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

	// --- SETUP INTERCEPTION ---
	if m.ActiveSetup != nil {
		var setupCmd tea.Cmd
		m.ActiveSetup, setupCmd = m.ActiveSetup.Update(msg)

		// Check for termination based on common patterns or specific messages
		done := false
		if s, ok := m.ActiveSetup.(*UserWizardModel); ok && s.Done {
			done = true
		}
		if p, ok := m.ActiveSetup.(*ProviderWizard); ok && p.done {
			done = true
		}
		if mw, ok := m.ActiveSetup.(*ModelWizard); ok && mw.done {
			done = true
		}
		if sw, ok := m.ActiveSetup.(*StartupWizard); ok && sw.Done {
			done = true
		}

		if done {
			m.ActiveSetup = nil
			if m.Ready {
				m.Viewport.SetContent(m.renderMarkdown(m.History))
				m.Viewport.GotoBottom()
			}
		}
		return m, setupCmd
	}

	switch msg := msg.(type) {
	case ClearStatusMsg:
		m.StatusMessage = ""
		return m, nil

	case SetupFinishedMsg:
		// Persist the results
		repo := db.NewUserRepository(m.appStore)
		user, err := repo.GetDefaultUser(context.Background())
		if err == nil {
			user.Name = msg.Result.Name
			user.Expertise = msg.Result.Expertise
			user.Interests = msg.Result.Interests
			user.Bio = msg.Result.Bio
			_ = repo.UpdateUser(context.Background(), user)
		}
		m.ActiveSetup = nil
		m.appendHistory(BlockComponent, setupDoneStyle.Render("✨ Profile saved! Vraxter is now personalized to your needs.\n\n"))
		if m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
		}
		return m, nil

	case cmdOutputMsg:
		rendered := m.renderCmdBlock(msg.input, msg.output)
		m.appendHistory(BlockComponent, rendered)
		if m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
			m.Viewport.GotoBottom()
		}
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

			if strings.HasPrefix(messageToSend, "/") {
				cmd := m.handleSlashCommand(messageToSend)
				if m.Ready {
					m.Viewport.SetContent(m.renderMarkdown(m.History))
					m.Viewport.GotoBottom()
				}
				return m, cmd
			}

			// Timeline Refactor: User Identity Block
			m.appendHistory(BlockMarkdown, messageToSend, SenderUser)

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

		if newRenderer, err := buildRenderer(msg.Width); err == nil {
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
					sender := SenderVraxter
					if m.CurrentFocus == "specialist" {
						sender = SenderSpecialist
					}
					m.appendHistory(BlockMarkdown, parts[0], sender)
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
					sender := SenderVraxter
					if m.CurrentFocus == "specialist" {
						sender = SenderSpecialist
					}
					// Re-ensure banner state if CoT was the first thing sent
					if !m.HasSentVraxterBanner {
						m.HasSentVraxterBanner = true
					}
					m.appendHistory(BlockMarkdown, parts[1], sender)
				}
				return m, nil
			}

			if !m.HasSentVraxterBanner && !m.isThinking {
				m.HasSentVraxterBanner = true
			}

			if m.isThinking {
				m.appendHistory(BlockThought, content)
			} else {
				sender := SenderVraxter
				if m.CurrentFocus == "specialist" {
					sender = SenderSpecialist
				}
				// Trim incoming tokens to avoid early newline drift
				m.appendHistory(BlockMarkdown, content, sender)
			}
		case llm.EventTypeStatus:
			m.StatusMessage = msg.Event.Content
			cmds = append(cmds, func() tea.Msg {
				time.Sleep(4 * time.Second)
				return ClearStatusMsg{}
			})
		case llm.EventTypeSkillCall:
			label := msg.Event.Content
			icon := "◆"
			if label == "vraxter-return-control" {
				label = "Specialist boundary reached. Resuming Supervisor context..."
				icon = "◈"
				m.CurrentFocus = "vraxter"     // ◈ Focus shifts for THIS turn
				m.HasSentVraxterBanner = false // Allow supervisor banner to spawn
				m.ActiveSpecialistID = ""      // Clear latched specialist
				m.SpecialistBanner = ""
			} else {
				label = fmt.Sprintf("running tool: %s...", label)
			}

			sigil := lipgloss.NewStyle().Foreground(colorGold).Render("  " + icon)
			text := lipgloss.NewStyle().Foreground(colorMutedGray).Italic(true).Render(" " + label)
			m.appendHistory(BlockComponent, sigil+text+"\n\n")
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

				// Prepend the Rich Specialist Banner state if not sent yet
				if !m.HasSentVraxterBanner {
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

func (m *Model) appendHistory(t BlockType, content string, sender ...SenderType) {
	s := SenderSystem
	if len(sender) > 0 {
		s = sender[0]
	}

	if len(m.History) > 0 && m.History[len(m.History)-1].Type == t && m.History[len(m.History)-1].Sender == s && (t == BlockMarkdown || t == BlockThought) {
		m.History[len(m.History)-1].Content += content
	} else {
		m.History = append(m.History, HistoryBlock{Type: t, Content: content, Sender: s})
	}
}

func splitArgs(input string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false
	quoteChar := rune(0)

	for _, r := range input {
		switch {
		case r == '"' || r == '\'':
			if inQuotes {
				if r == quoteChar {
					inQuotes = false
				} else {
					current.WriteRune(r)
				}
			} else {
				inQuotes = true
				quoteChar = r
			}
		case r == ' ' && !inQuotes:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func (m *Model) handleSlashCommand(cmd string) tea.Cmd {
	parts := splitArgs(cmd)
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
		help := "\n🤖 Vraxter TUI Commands:" + helpCommand

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
	case "/start":
		pRepo := db.NewProviderRepository(m.appStore, m.appCrypto)
		mRepo := db.NewModelRepository(m.appStore, m.appCrypto)
		pMgr := services.NewProviderManager(pRepo)
		mMgr := services.NewModelManager(mRepo, pRepo)
		m.ActiveSetup = NewStartupWizard(pMgr, mMgr)
		return nil
	case "/providers", "/models", "/skills", "/add", "/specialists", "/user":
		if cmd == "/user setup" && len(parts) == 2 && parts[1] == "setup" {
			m.ActiveSetup = NewUserWizardModel()
			return nil
		}
		if cmd == "/providers setup" && len(parts) == 2 && parts[1] == "setup" {
			repo := db.NewProviderRepository(m.appStore, m.appCrypto)
			mgr := services.NewProviderManager(repo)
			m.ActiveSetup = NewProviderWizard(mgr)
			return nil
		}
		if cmd == "/models setup" && len(parts) == 2 && parts[1] == "setup" {
			pRepo := db.NewProviderRepository(m.appStore, m.appCrypto)
			mRepo := db.NewModelRepository(m.appStore, m.appCrypto)
			pMgr := services.NewProviderManager(pRepo)
			mMgr := services.NewModelManager(mRepo, pRepo)
			m.ActiveSetup = NewModelWizard(pMgr, mMgr)
			return nil
		}

		// Intercept /providers <id> discover for native rich rendering
		if parts[0] == "/providers" && len(parts) == 3 && parts[2] == "discover" {
			pID := parts[1]
			return func() tea.Msg {
				repo := db.NewProviderRepository(m.appStore, m.appCrypto)
				mgr := services.NewProviderManager(repo)
				models, err := mgr.DiscoverModels(m.ctx, pID)
				if err != nil {
					return cmdOutputMsg{input: cmd, output: fmt.Sprintf("❌ Discovery failed: %v", err)}
				}

				rendered := m.renderDiscoveryRich(pID, models)
				return cmdOutputMsg{input: cmd, output: rendered}
			}
		}

		execArgs := append([]string{strings.TrimPrefix(parts[0], "/")}, parts[1:]...)
		cmdInput := cmd

		return func() tea.Msg {
			goCmd := exec.Command(os.Args[0], execArgs...)
			goCmd.Env = append(os.Environ(), "VRAXTER_INTERNAL_SESSION=true")
			out, _ := goCmd.CombinedOutput()

			cleanOut := strings.ReplaceAll(string(out), "Type a command for Vraxter... (Ctrl+C to quit)", "")
			cleanOut = stripANSI(strings.TrimSpace(cleanOut))

			return cmdOutputMsg{input: cmdInput, output: cleanOut}
		}
	default:
		m.appendHistory(BlockComponent, errorStyle.Render(fmt.Sprintf("❌ Unknown command: %s. Type /help for options.\n\n", parts[0])))
		return nil
	}
}

func (m *Model) renderDiscoveryRich(pID string, models []interfaces.ModelMetadata) string {
	width := m.width - 6

	styleGold := lipgloss.NewStyle().Foreground(colorGold).Bold(true)
	styleID := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8"))
	styleMuted := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	styleHeader := lipgloss.NewStyle().Foreground(lipgloss.Color("#777777"))
	styleCaps := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	styleDivider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2a2a2a"))
	bracket := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a3a1a")).Render
	ctxVal := lipgloss.NewStyle().Foreground(colorEmerald).Bold(true)

	ctxBadge := func(ctx int) string {
		var label string
		if ctx <= 0 {
			return styleMuted.Render("[N/A]")
		} else if ctx >= 1_000_000 {
			label = fmt.Sprintf("%.0fM", float64(ctx)/1_000_000)
		} else if ctx >= 1_000 {
			label = fmt.Sprintf("%dk", ctx/1000)
		} else {
			label = fmt.Sprintf("%d", ctx)
		}
		return bracket("[") + ctxVal.Render(label) + bracket("]")
	}

	// Agrupar modelos por tipo
	groups := make(map[string][]interfaces.ModelMetadata)
	for _, mod := range models {
		group := "LLM (Text)"
		for _, c := range mod.Capabilities {
			if c == "embedding" {
				group = "Embedding"
				break
			}
			if c == "audio" {
				group = "Audio"
				break
			}
			if c == "image" {
				group = "Image"
				break
			}
		}
		groups[group] = append(groups[group], mod)
	}

	var sb strings.Builder

	// Título
	sb.WriteString(styleGold.Render(
		fmt.Sprintf("◆ REMOTE MODELS · %s", strings.ToUpper(pID)),
	) + "\n\n")

	// Header de columnas
	const idW = 42
	const ctxW = 8
	sb.WriteString(
		styleHeader.Render(fmt.Sprintf(
			"  %-*s  %-*s  %s",
			idW, "MODEL ID",
			ctxW, "CONTEXT",
			"CAPABILITIES",
		)) + "\n",
	)
	sb.WriteString(styleDivider.Render(strings.Repeat("─", width-3)) + "\n")

	order := []string{"LLM (Text)", "Embedding", "Audio", "Image"}
	for _, gTitle := range order {
		ms := groups[gTitle]
		if len(ms) == 0 {
			continue
		}

		// Header de grupo
		sb.WriteString("\n" + styleMuted.Render(fmt.Sprintf("── %s", gTitle)) + "\n")

		for _, mod := range ms {
			badge := ctxBadge(mod.ContextWindow)

			// ID — pad manual para alinear columna
			idRendered := styleID.Render(mod.ID)
			idPad := idW - len(mod.ID) // usamos len del string raw para el pad
			if idPad < 0 {
				idPad = 0
			}

			// Context badge — ancho fijo
			// El badge visual es [XY] = 2 brackets + label
			// Calculamos el raw label width para el pad
			rawBadgeW := len(fmt.Sprintf("[%s]", func() string {
				if mod.ContextWindow <= 0 {
					return "N/A"
				}
				if mod.ContextWindow >= 1_000_000 {
					return fmt.Sprintf("%.0fM", float64(mod.ContextWindow)/1_000_000)
				}
				if mod.ContextWindow >= 1_000 {
					return fmt.Sprintf("%dk", mod.ContextWindow/1000)
				}
				return fmt.Sprintf("%d", mod.ContextWindow)
			}()))
			ctxPad := ctxW - rawBadgeW
			if ctxPad < 1 {
				ctxPad = 1
			}

			// Capabilities
			capsSlice := make([]string, len(mod.Capabilities))
			for i, c := range mod.Capabilities {
				capsSlice[i] = styleCaps.Render(c)
			}
			caps := strings.Join(capsSlice, styleMuted.Render(" · "))

			sb.WriteString(fmt.Sprintf("  %s%s  %s%s  %s\n",
				idRendered,
				strings.Repeat(" ", idPad),
				badge,
				strings.Repeat(" ", ctxPad),
				caps,
			))
		}
	}

	// Footer
	sb.WriteString("\n" + styleDivider.Render(strings.Repeat("─", width-3)) + "\n")
	sb.WriteString(styleMuted.Render(fmt.Sprintf(
		"  use /providers %s model <id> --add to insert it into vraxter", pID,
	)))

	return sb.String()
}

func (m *Model) renderCmdBlock(input, output string) string {
	width := m.width - 6

	slashStyle := lipgloss.NewStyle().Foreground(colorGold).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8"))
	metaStyle := lipgloss.NewStyle().Foreground(colorMutedGray)
	bodyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7a8090")).
		PaddingLeft(1).
		PaddingTop(1).
		PaddingBottom(1).
		Width(width - 2)

	// Sin background en el header — solo el texto con padding
	headerStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Width(width)

	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#1e2530"))

	lines := strings.Count(output, "\n")
	meta := metaStyle.Render(fmt.Sprintf("%d lines", lines))

	cmdText := slashStyle.Render("/ ") + cmdStyle.Render(input[1:])
	pad := width - 4 - lipgloss.Width(cmdText) - lipgloss.Width(meta)
	if pad < 1 {
		pad = 1
	}

	header := headerStyle.Render(cmdText + strings.Repeat(" ", pad) + meta)
	divider := dividerStyle.Render(strings.Repeat("─", width-2))
	body := bodyStyle.Render(output)

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#1e2530")).
		Width(width).
		MarginBottom(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, divider, body)) + "\n"
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

	boxWidth := width - 10
	if boxWidth < 50 {
		boxWidth = 50
	}

	accentStyle := lipgloss.NewStyle().Foreground(colorGold).Bold(true)
	phaseStyle := lipgloss.NewStyle().Foreground(colorEmerald).Bold(true)
	assigneeStyle := lipgloss.NewStyle().Foreground(colorOrange)
	opaqeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#999999")).Faint(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8"))
	mutedStyle := lipgloss.NewStyle().Foreground(colorMutedGray).Faint(true)
	divider := mutedStyle.Render(strings.Repeat("─", boxWidth-6))

	doc := strings.Builder{}

	taskSnippet := plan.Goal
	if len(taskSnippet) > 55 {
		taskSnippet = taskSnippet[:55] + "…"
	}
	phaseBadge := fmt.Sprintf("%d phases · %s", len(plan.Phases), plan.Estimations)
	headerLeft := accentStyle.Render("◈ PLAN PROPOSAL") +
		lipgloss.NewStyle().Foreground(colorMutedGray).Render(" | "+taskSnippet)
	headerRight := opaqeStyle.
		Border(lipgloss.Border{Left: "┃"}, false, false, false, true).
		BorderForeground(colorMutedGray).
		Background(lipgloss.Color("#161C1D")).
		PaddingLeft(1).
		Render(phaseBadge)
	pad := boxWidth - 6 - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if pad < 1 {
		pad = 1
	}
	doc.WriteString(headerLeft + strings.Repeat(" ", pad) + headerRight + "\n")
	doc.WriteString(divider)

	// NARRATIVE
	if plan.Narrative != "" {
		empty := ""
		zero := uint(0)
		gold := string(colorGold)
		emerald := string(colorEmerald)
		orange := string(colorOrange)

		ns := styles.DarkStyleConfig
		ns.Document.BackgroundColor = &empty
		ns.Document.Margin = &zero
		ns.Paragraph.BackgroundColor = &empty
		ns.Paragraph.Margin = &zero
		ns.Text.BackgroundColor = &empty
		ns.Heading.BackgroundColor = &empty
		ns.Heading.Margin = &zero
		ns.H1.BackgroundColor = &empty
		ns.H2.BackgroundColor = &empty
		ns.H3.BackgroundColor = &empty
		ns.H1.Color = &gold
		ns.H2.Color = &gold
		ns.H3.Color = &gold
		ns.Strong.Color = &emerald
		ns.Emph.Color = &orange
		ns.Item.Color = &empty
		ns.List.BackgroundColor = &empty
		ns.Item.BackgroundColor = &empty

		r, _ := glamour.NewTermRenderer(
			glamour.WithStyles(ns),
			glamour.WithWordWrap(boxWidth-6),
		)
		rendered, _ := r.Render(plan.Narrative)
		rendered = ansibg.ReplaceAllString(rendered, "")
		rendered = strings.ReplaceAll(rendered, "\x1b[0m", "\x1b[39m")
		doc.WriteString(rendered)
		doc.WriteString(divider + "\n")
	}

	// GOAL + REASONING
	goalText := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c8c8c8")).
		Width(boxWidth - 14).
		Render(plan.Goal)
	doc.WriteString(accentStyle.Render("🎯 Goal:  ") + goalText + "\n")

	reasonText := lipgloss.NewStyle().
		Foreground(colorMutedGray).
		Width(boxWidth - 14).
		Render(plan.Reasoning)
	doc.WriteString(accentStyle.Italic(true).Render("🤔 Why:   ") + reasonText + "\n")

	doc.WriteString(divider + "\n")

	// PHASES
	for i, p := range plan.Phases {
		// Phase header
		doc.WriteString(fmt.Sprintf("%s %d  %s\n",
			phaseStyle.Render("Phase"),
			i+1,
			textStyle.Bold(true).Render(p.Title),
		))

		// Description
		if p.Description != "" {
			doc.WriteString(descStyle.Render("   "+p.Description) + "\n")
		}

		// Tasks
		for _, t := range p.Tasks {
			doc.WriteString(opaqeStyle.Render(fmt.Sprintf("   ─ %s", t)) + "\n")
		}

		// Assignee
		doc.WriteString(
			assigneeStyle.Bold(true).Render("   ◆ ") +
				assigneeStyle.Render("Asignee: ") +
				lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8")).Bold(true).Render(p.Specialist) + "\n",
		)

		if i < len(plan.Phases)-1 {
			doc.WriteString("\n")
		}
	}

	doc.WriteString("\n" + divider + "\n")
	doc.WriteString(accentStyle.Render("⏳ Estimate: ") + opaqeStyle.Render(plan.Estimations))

	if status != "" {
		doc.WriteString("\n" + divider + "\n")
		statusStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		if strings.Contains(status, "ACCEPTED") {
			statusStyle = statusStyle.Foreground(colorEmerald)
		} else {
			statusStyle = statusStyle.Foreground(lipgloss.Color("#FF5252"))
		}
		doc.WriteString(statusStyle.Render(status))
	} else if m.IsWaitingForApproval {
		doc.WriteString("\n" + divider + "\n")

		labels := []string{"PROCEED", "REJECT", "AMEND"}
		colors := []lipgloss.Color{
			colorEmerald,
			lipgloss.Color("#FF5252"),
			colorGold,
		}

		var parts []string
		for i, label := range labels {
			s := lipgloss.NewStyle().Foreground(colors[i]).Bold(i == m.PlanActionIndex)
			if i == m.PlanActionIndex {
				s = s.Reverse(true)
			} else {
				s = s.Faint(true)
			}
			parts = append(parts, s.Render(" "+label+" "))
		}

		hintStyle := lipgloss.NewStyle().Foreground(colorMutedGray).Faint(true)
		doc.WriteString(hintStyle.Render("◈ ") + strings.Join(parts, "  "))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a2a2a")).
		Padding(1, 2).
		Width(boxWidth).
		Render(doc.String())
}

func (m *Model) renderDecisionBridge(width int) string {
	btnStyle := lipgloss.NewStyle().Bold(true).Padding(0, 3).MarginRight(1)

	proceedBase := btnStyle.Copy().Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#1B5E20")).Foreground(lipgloss.Color("#1B5E20"))
	rejectBase := btnStyle.Copy().Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#B71C1C")).Foreground(lipgloss.Color("#B71C1C"))
	amendBase := btnStyle.Copy().Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#FF8F00")).Foreground(lipgloss.Color("#FF8F00"))

	if m.PlanActionIndex == 0 {
		proceedBase = proceedBase.Copy().Background(colorEmerald).
			Foreground(colorOnyx).BorderForeground(colorEmerald)
	} else if m.PlanActionIndex == 1 {
		rejectBase = rejectBase.Copy().Background(lipgloss.Color("#FF5252")).
			Foreground(colorOnyx).BorderForeground(lipgloss.Color("#FF5252"))
	} else if m.PlanActionIndex == 2 {
		amendBase = amendBase.Copy().Background(lipgloss.Color("#FFD54F")).
			Foreground(colorOnyx).BorderForeground(lipgloss.Color("#FFD54F"))
	}

	buttons := lipgloss.JoinHorizontal(lipgloss.Top,
		proceedBase.Render("PROCEED"),
		rejectBase.Render("REJECT"),
		amendBase.Render("AMEND"),
	)

	content := buttons
	if m.ShowAmendInput {
		m.Input.Prompt = "❯ FEEDBACK: "
		m.Input.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
		content = lipgloss.JoinVertical(lipgloss.Left,
			buttons,
			lipgloss.NewStyle().PaddingTop(1).Render(m.Input.View()),
		)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(lipgloss.Color("#2a2a2a")). // más sutil que gold
		Padding(1, 0).
		Width(width).
		Render(content)
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
	if m.ActiveSetup != nil {
		if s, ok := m.ActiveSetup.(*UserWizardModel); ok {
			s.Width = m.width
		}
		if p, ok := m.ActiveSetup.(*ProviderWizard); ok {
			p.width = m.width
		}
		if mw, ok := m.ActiveSetup.(*ModelWizard); ok {
			mw.width = m.width
		}
		if sw, ok := m.ActiveSetup.(*StartupWizard); ok {
			sw.width = m.width
		}
		return m.ActiveSetup.View()
	}

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

	for _, block := range history {
		var content string
		switch block.Type {
		case BlockMarkdown:
			content = block.Content
			content = stripANSI(content)

			// --- PRO-CODE SPLITTER LOGIC ---
			lastIndex := 0
			matches := codeBlockRe.FindAllStringSubmatchIndex(content, -1)

			var messageText strings.Builder
			for _, match := range matches {
				// 1. Render text before the code block
				if match[0] > lastIndex {
					textSegment := content[lastIndex:match[0]]

					out := m.renderMarkdownString(textSegment)
					messageText.WriteString(out)
				}

				// 2. Render the Code Block as a specialized Component
				lang := ""
				if match[2] != -1 && match[3] != -1 {
					lang = content[match[2]:match[3]]
				}
				code := content[match[4]:match[5]]

				messageText.WriteString(m.renderCodeBlock(lang, code, m.width-12))

				lastIndex = match[1]
			}

			// 3. Render remaining text
			if lastIndex < len(content) {
				textSegment := content[lastIndex:]
				out := m.renderMarkdownString(textSegment)
				messageText.WriteString(out)
			}

			// --- BLOCK WRAPPER ---
			// Trim Space to eliminate trailing newlines from Glamour or appends
			finalBlockContent := strings.TrimSpace(messageText.String())
			if finalBlockContent == "" {
				continue
			}

			switch block.Sender {
			case SenderUser:
				fullContent.WriteString(userBlockStyle.Width(m.width-6).Render(finalBlockContent) + "\n")
			case SenderVraxter:
				fullContent.WriteString(vraxterBlockStyle.Width(m.width-6).Render(finalBlockContent) + "\n")
			case SenderSpecialist:
				fullContent.WriteString(specialistBlockStyle.Width(m.width-6).Render(finalBlockContent) + "\n")
			default:
				fullContent.WriteString(finalBlockContent + "\n")
			}

		case BlockThought:
			fullContent.WriteString(thoughtStyle.Render(block.Content) + "\n\n")

		case BlockComponent:
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

func (m *Model) renderMarkdownString(content string) string {

	out, err := m.renderer.Render(content)
	if err != nil {
		return content
	}

	out = ansibg.ReplaceAllString(out, "")
	// Replace Global Reset (\x1b[0m) with Foreground Reset (\x1b[39m)
	// to avoid clearing the Lipgloss block's background color.
	out = strings.ReplaceAll(out, "\x1b[0m", "\x1b[39m")
	return out
}

func (m *Model) renderCodeBlock(lang, code string, width int) string {
	if lang == "" {
		lang = "CODE"
	}

	// 1. Clean the lang string of any rogue ANSI to prevent background corruption in the header
	reANSI := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lang = reANSI.ReplaceAllString(lang, "")
	lang = strings.ToUpper(lang)

	var inner string
	if lang == "CODE" {
		inner = strings.TrimSpace(code)
	} else {
		// Use Chroma directly instead of Glamour.
		// Glamour renders a full Markdown block (adding margins, padding, and background spaces),
		// which collides with your custom Lipgloss wrapper.
		var buf bytes.Buffer

		// You can change "dracula" to any Chroma theme you prefer (e.g., "monokai", "nord").
		err := quick.Highlight(&buf, strings.TrimSpace(code), strings.ToLower(lang), "terminal16m", "dracula")

		if err == nil {
			inner = buf.String()
			// Strip Chroma's background colors so your Lipgloss background "#080b0f" shines through cleanly
			inner = utils.StripANSIBackgrounds(inner)

			// Prevent global reset from clearing Lipgloss's background color mid-line.
			// We replace Chroma's reset (\x1b[0m) with a full reset followed by the default
			// foreground (#abb2bf) and background (#080b0f) of the code block.
			restoreSeq := "\x1b[0m\x1b[38;2;171;178;191m\x1b[48;2;8;11;15m"
			inner = strings.ReplaceAll(inner, "\x1b[0m", restoreSeq)
		} else {
			inner = strings.TrimSpace(code)
		}
	}

	// 2. Manually pad EVERY line to ensure the background stretches fully across
	// without triggering Lipgloss's internal ANSI-padding bugs.
	lines := strings.Split(inner, "\n")
	paddedWidth := width - 4 // width - 2 (Border) - 2 (PaddingLeft)
	for i, line := range lines {
		visibleLen := lipgloss.Width(line)
		if paddedWidth > 0 && visibleLen < paddedWidth {
			lines[i] = line + strings.Repeat(" ", paddedWidth-visibleLen)
		}
	}
	inner = strings.Join(lines, "\n")

	dotStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#d4af37")) // colorGold
	langStyle := dotStyle.Bold(true)

	// 3. Header strictly padded without using Width() to prevent Lipgloss reflow bugs
	headerText := dotStyle.Render("◆ ") + langStyle.Render(lang)
	// Replace internal resets so the header's #0a0d12 background isn't prematurely cleared
	headerText = strings.ReplaceAll(headerText, "\x1b[0m", "\x1b[39m\x1b[48;2;10;13;18m")

	headerVisibleLen := lipgloss.Width(headerText)
	headerPaddedWidth := width - 3
	if headerPaddedWidth > 0 && headerVisibleLen < headerPaddedWidth {
		headerText += strings.Repeat(" ", headerPaddedWidth-headerVisibleLen)
	}

	header := lipgloss.NewStyle().
		Background(lipgloss.Color("#0a0d12")).
		PaddingLeft(1).
		Width(width).
		Render(headerText)

	// 4. Body strictly padded without using Width() to prevent right-side black gaps
	body := lipgloss.NewStyle().
		Background(lipgloss.Color("#080b0f")).
		Foreground(lipgloss.Color("#abb2bf")).
		PaddingLeft(2).
		Width(width).
		Render(inner)

	// 5. Divider strictly width-2 WITH the #080b0f background
	divider := lipgloss.NewStyle().
		Background(lipgloss.Color("#080b0f")).
		Foreground(lipgloss.Color("#1e2530")).
		Render(strings.Repeat("─", width))

	// 6. Outer wrapper strictly controls the border, NO outer background
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#1e2530")).
		Width(width).
		MarginBottom(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, divider, body)) + "\n"
}

func stripANSI(str string) string {
	const ansi = "[\u001B\u009B][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]"
	var re = regexp.MustCompile(ansi)
	return re.ReplaceAllString(str, "")
}

func buildRenderer(width int) (*glamour.TermRenderer, error) {
	empty := ""
	zero := uint(0)

	s := styles.DarkStyleConfig

	// Document
	s.Document.BackgroundColor = &empty
	s.Document.Margin = &zero

	// Paragraph - The main culprit for background-per-line artifacts
	s.Paragraph.BackgroundColor = &empty
	s.Paragraph.Margin = &zero

	// Text base
	s.Text.BackgroundColor = &empty

	// Headings
	s.Heading.BackgroundColor = &empty
	s.Heading.Margin = &zero
	s.H1.BackgroundColor = &empty
	s.H2.BackgroundColor = &empty
	s.H3.BackgroundColor = &empty
	s.H4.BackgroundColor = &empty

	// Lists
	s.List.BackgroundColor = &empty
	s.Item.BackgroundColor = &empty

	// Blockquote
	s.BlockQuote.BackgroundColor = &empty
	s.BlockQuote.Margin = &zero
	s.BlockQuote.Indent = &zero

	// Code inline - Transparent base
	s.Code.BackgroundColor = &empty

	// Code block - Intentional dark background
	s.CodeBlock.BackgroundColor = &empty
	s.CodeBlock.Margin = &zero

	// Table
	s.Table.BackgroundColor = &empty

	// boxWidth safety margin (Width-12 to account for block sidebars/padding)
	return glamour.NewTermRenderer(
		glamour.WithStyles(s),
		glamour.WithWordWrap(width-12),
	)
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
		faintStyle.Render("• /start to configure everything at once"),
		faintStyle.Render("• /plan <task> for autonomous mode"),
		faintStyle.Render("• Type ? for quick command help"),
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
