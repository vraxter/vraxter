// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

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

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2/quick"
	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

const vraxterIconPlaceholder = "[[VRAXTER_IDENTITY]]"

var (
	// Anodized Titanium & Obsidian Chassis
	colorVoid          = lipgloss.Color("#07080A")
	colorObsidian      = lipgloss.Color("#0B0D10")
	colorTitaniumDark  = lipgloss.Color("#13161B")
	colorTitaniumMid   = lipgloss.Color("#1C2028")
	colorTitaniumLight = lipgloss.Color("#2A303C")

	// Brushed & Burnished Copper Palette
	colorCopperSpecular   = lipgloss.Color("#F7D4B2")
	colorCopperLight      = lipgloss.Color("#E2A576")
	colorCopper           = lipgloss.Color("#C87D4B")
	colorCopperMid        = lipgloss.Color("#A65E30")
	colorCopperDeep       = lipgloss.Color("#6E3918")
	colorCopperLine       = lipgloss.Color("#35241A") // rgba(200, 125, 75, 0.24) over #07080A
	colorCopperLineStrong = lipgloss.Color("#70533E") // rgba(226, 165, 118, 0.48) over #07080A

	// Compound Optical Turret Sapphire & Micro-LED Jewels
	colorSapphireBright = lipgloss.Color("#5B8FD9")
	colorSapphireMid    = lipgloss.Color("#2C548E")
	colorSapphireDeep   = lipgloss.Color("#102342")
	colorLedEmerald     = lipgloss.Color("#3CE68A")
	colorLedEmeraldGlow = lipgloss.Color("#00CC55")
	colorLedAmber       = lipgloss.Color("#F5A623")
	colorLedCrimson     = lipgloss.Color("#FF5252")

	// Editorial Typography Colors
	colorIvory = lipgloss.Color("#F4F1EA")
	colorStone = lipgloss.Color("#B8B5AE")
	colorAsh   = lipgloss.Color("#75777E")
	colorSlate = lipgloss.Color("#454952")

	// TUI Surface Background Hex Tokens (from TUI CSS)
	hexTuiBg             = "#08090C" // .tui-monolith
	hexHeaderBg          = "#111318" // .tui-welcome-header
	hexUserBlockBg       = "#101216" // .tui-msg-user
	hexVraxterBlockBg    = "#111012" // .tui-msg-vraxter
	hexSpecialistBlockBg = "#130F0F" // .tui-msg-specialist
	hexCodeBg            = "#050608" // .tui-code-sandbox
	hexCodeHeaderBg      = "#0B0D10" // --obsidian
	hexApprovalCardBg    = "#14110E" // .tui-approval-request-card
	hexAutocompleteBg    = "#0C0E12" // .tui-autocomplete-overlay
	hexAutocompleteSelBg = "#271E18" // .aco-row.selected
	hexCommandDeckBg     = "#0A0C10" // .tui-command-deck
	hexFocusPillBg       = "#161920" // .rail-focus-pill
	hexInputBg           = "#0D1015" // .tui-input-shelf
	hexInactiveBadgeBg   = "#12151C" // .tui-inactive-badge
	hexApprovalShelfBg   = "#171310" // .tui-input-shelf.is-approval

	hexVoid             = "#07080A"
	hexObsidian         = "#0B0D10"
	hexTitaniumDark     = "#13161B"
	hexTitaniumMid      = "#1C2028"
	hexTitaniumLight    = "#2A303C"
	hexCopperSpecular   = "#F7D4B2"
	hexCopperLight      = "#E2A576"
	hexCopper           = "#C87D4B"
	hexCopperMid        = "#A65E30"
	hexCopperLine       = "#35241A"
	hexCopperLineStrong = "#70533E"
	hexSapphireBright   = "#5B8FD9"
	hexSapphireMid      = "#2C548E"
	hexSapphireDeep     = "#102342"
	hexLedEmerald       = "#3CE68A"
	hexLedAmber         = "#F5A623"
	hexLedCrimson       = "#FF5252"
	hexIvory            = "#F4F1EA"
	hexStone            = "#B8B5AE"
	hexAsh              = "#75777E"
	hexSlate            = "#454952"

	colorTuiBg             = lipgloss.Color(hexTuiBg)
	colorHeaderBg          = lipgloss.Color(hexHeaderBg)
	colorUserBlockBg       = lipgloss.Color(hexUserBlockBg)
	colorVraxterBlockBg    = lipgloss.Color(hexVraxterBlockBg)
	colorSpecialistBlockBg = lipgloss.Color(hexSpecialistBlockBg)
	colorCodeBg            = lipgloss.Color(hexCodeBg)
	colorApprovalCardBg    = lipgloss.Color(hexApprovalCardBg)
	colorAutocompleteBg    = lipgloss.Color(hexAutocompleteBg)
	colorAutocompleteSelBg = lipgloss.Color(hexAutocompleteSelBg)
	colorCommandDeckBg     = lipgloss.Color(hexCommandDeckBg)
	colorFocusPillBg       = lipgloss.Color(hexFocusPillBg)
	colorInputBg           = lipgloss.Color(hexInputBg)
	colorInactiveBadgeBg   = lipgloss.Color(hexInactiveBadgeBg)
	colorApprovalShelfBg   = lipgloss.Color(hexApprovalShelfBg)

	// Semantic Aliases
	colorOnyx      = colorVoid
	colorDeepGray  = colorTitaniumDark
	colorGold      = colorCopperLight
	colorOrange    = colorCopper
	colorEmerald   = colorLedEmerald
	colorMutedGray = colorAsh
	colorUser      = colorStone

	appStyle = lipgloss.NewStyle().
			Background(colorTuiBg)

	headerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorCopper).
			Background(colorHeaderBg).
			Padding(1, 2)

	tipsBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorCopperMid).
			Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Foreground(colorIvory).
			Bold(true)

	userStyle = lipgloss.NewStyle().
			Foreground(colorCopper).
			Bold(true)

	agentStyle = lipgloss.NewStyle().
			Foreground(colorLedEmerald).
			Bold(true)

	agentIconStyle = lipgloss.NewStyle().
			Foreground(colorLedEmerald).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorLedCrimson).
			Bold(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(colorCopperLight).
			Italic(true)

	faintStyle = lipgloss.NewStyle().
			Foreground(colorAsh)

	statusBarStyle = lipgloss.NewStyle().
			Background(colorTitaniumDark).
			Foreground(colorCopperLight).
			Bold(true).
			Padding(0, 1)

	thoughtStyle = lipgloss.NewStyle().
			Foreground(colorAsh).
			Italic(true)

	// --- Block Message Styles ---
	userBlockStyle = lipgloss.NewStyle().
			Border(lipgloss.Border{Left: "│"}, false, false, false, true).
			BorderForeground(colorSlate).
			Background(colorUserBlockBg).
			PaddingLeft(2).
			PaddingRight(2).
			PaddingTop(1).
			PaddingBottom(1).
			MarginBottom(1)

	vraxterBlockStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "│"}, false, false, false, true).
				BorderForeground(colorCopperSpecular).
				Background(colorVraxterBlockBg).
				PaddingLeft(2).
				PaddingRight(2).
				PaddingTop(1).
				PaddingBottom(1).
				MarginBottom(1)

	specialistBlockStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "│"}, false, false, false, true).
				BorderForeground(colorCopperMid).
				Background(colorSpecialistBlockBg).
				PaddingLeft(2).
				PaddingRight(2).
				PaddingTop(1).
				PaddingBottom(1).
				MarginBottom(1)

	inputShelfStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorCopperLineStrong).
			Background(colorInputBg)

	promptStyle = lipgloss.NewStyle().
			Foreground(colorCopperLight).
			Bold(true).
			Padding(0, 1)

	sigilSpinner = spinner.Spinner{
		Frames: buildSigilSpinnerFrames(),
		FPS:    time.Millisecond * 120,
	}

	ansibg      = regexp.MustCompile("\x1b\\[(4[0-9]|10[0-9]|48;[0-9];[0-9;]+)m")
	sgrSeqRe    = regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	codeBlockRe = regexp.MustCompile("(?s)```(\\w+)?\n(.*?)\n```")
)

// buildSigilSpinnerFrames builds a 10-frame 2x4 Sigil animation where all 8 strokes
// (\/\/ on row 0, /\/\ on row 1) are always present at fixed coordinates so the height (2 rows)
// and width (4 cols) never jump, while a copper-specular beam progressively illuminates the bars.
func buildSigilSpinnerFrames() []string {
	glyphs := [8]string{`\`, `/`, `\`, `/`, `/`, `\`, `/`, `\`}
	dimStyle := lipgloss.NewStyle().Foreground(colorSlate)
	litStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	headStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)

	renderFrame := func(styles [8]lipgloss.Style) string {
		var r0, r1 strings.Builder
		for i := 0; i < 4; i++ {
			r0.WriteString(styles[i].Render(glyphs[i]))
		}
		for i := 4; i < 8; i++ {
			r1.WriteString(styles[i].Render(glyphs[i]))
		}
		return r0.String() + "\n" + r1.String()
	}

	frames := make([]string, 0, 10)
	for step := 0; step < 8; step++ {
		var st [8]lipgloss.Style
		for i := 0; i < 8; i++ {
			switch {
			case i < step:
				st[i] = litStyle
			case i == step:
				st[i] = headStyle
			default:
				st[i] = dimStyle
			}
		}
		frames = append(frames, renderFrame(st))
	}

	var allSpec, allLit [8]lipgloss.Style
	for i := 0; i < 8; i++ {
		allSpec[i] = headStyle
		allLit[i] = litStyle
	}
	frames = append(frames, renderFrame(allSpec), renderFrame(allLit))
	return frames
}

var helpCommand = `
  /activate <id>		- Manually switch and lock the active model for this session
  /restore, /auto		- Restore automatic use-case based model routing
  /plan <task>		- Break down a complex task into a multi-phase plan
  /history		- Browse your past conversation sessions
  /load <#>		- Load a past conversation by its row number from /history
  /clear		- Clears the chat history from the screen
  /new, /reset		- Generates a fresh session ID and clears history
  /session <id>		- Switches your context to a different session ID
  /jobs			- List all background tasks currently running
  /providers [args]	- Manage AI providers (/providers setup for wizard)
  /models [args]		- Manage models linked to providers
  /skills [args]		- Proxy: Run 'vraxter skills'
  /specialists [args]		- Proxy: Manage specialists
  /user [args]		- Proxy: Manage profile
  /add [args]		- Proxy: Quickly add skills
  /start		- Unified startup wizard (Profile ➔ Providers ➔ Models)
  /factory-reset	- Full factory reset (use /factory-reset --confirm)
  /quit, /exit		- Exits Vraxter
  /help, /?		- Shows this menu
`

var slashCommandsList = []string{
	"/activate", "/restore", "/auto", "/plan", "/history", "/load", "/clear",
	"/new", "/reset", "/session", "/jobs", "/providers", "/models", "/skills",
	"/specialists", "/user", "/add", "/start", "/factory-reset", "/quit", "/exit", "/help",
	"/airgap", "/isolation",
}

type EventMsgWrapper struct {
	Event llm.StreamEvent
	Next  func() tea.Msg
}

type SystemEventMsgWrapper struct {
	Event *v1.SystemEvent
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

type AutocompleteState struct {
	IsActive bool
	Type     string // "command" or "specialist"
	Items    []string
	Cursor   int
	Prefix   string
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
	FailoverModel        string
	OverrideModelID      string // Explicit override requested by user
	BaseModelID          string // Default from server
	grpcClient           *client.GRPCClient
	ctx                  context.Context
	ActiveSpecialistID   string // The specialist currently 'latched' to the session
	SpecialistBanner     string // The display name for the active specialist banner
	systemEvents         <-chan *v1.SystemEvent
	ActiveJobs           []*v1.JobStatus // Currently running background daemons

	// Rendering
	renderer *glamour.TermRenderer

	// Feedback & Selection
	MouseSelectionMode bool
	StatusMessage      string

	// Autocomplete
	Autocomplete         AutocompleteState
	AvailableSpecialists []string

	// Input History
	InputHistory    []string
	InputHistoryIdx int

	viewportY int // Dynamic top-offset for hit detection

	// Planning & Manual Execution
	IsPlanning           bool
	IsWaitingForApproval bool
	ProposedPlan         string // JSON content of the proposed plan
	PlanActionIndex      int    // 0:Proceed, 1:Reject, 2:Amend
	ShowAmendInput       bool
	PendingRunSkill      string // Name of skill awaiting manual run prompt
	ActiveCardHistoryIdx int    // Exact History index of the active Plan/Skill Approval card

	IsWaitingForSkillApproval bool
	PendingSkillPayload       string

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

	// Isolation & Airgap Shield
	IsolationMode  string
	IsAirgapLocked bool
}

func NewModel(ctx context.Context, gClient *client.GRPCClient, store *db.Store, crypto *security.CryptoService, session, initialQuery string, userName string) *Model {
	ta := textarea.New()
	ta.Placeholder = "Type a command for Vraxter..."
	ta.Prompt = "❯ "
	ta.Focus()
	ta.SetWidth(60)
	ta.SetHeight(1) // Start at one row

	// Match background to the Input Shelf (#0D1015) so there is no background mixing
	tas := ta.Styles()
	tas.Focused.Base = lipgloss.NewStyle().Background(colorInputBg)
	tas.Blurred.Base = lipgloss.NewStyle().Background(colorInputBg)

	// Styled prompt: Bold Copper Light
	tas.Focused.Prompt = lipgloss.NewStyle().Foreground(colorCopperLight).Background(colorInputBg).Bold(true)
	tas.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorSlate).Background(colorInputBg)

	// Ensure cursor and text styling share the exact input background
	tas.Cursor.Color = colorCopperSpecular
	tas.Focused.CursorLine = lipgloss.NewStyle().Background(colorInputBg)
	tas.Focused.Text = lipgloss.NewStyle().Foreground(colorIvory).Background(colorInputBg)
	tas.Blurred.Text = lipgloss.NewStyle().Foreground(colorAsh).Background(colorInputBg)
	tas.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorAsh).Background(colorInputBg)
	tas.Blurred.Placeholder = lipgloss.NewStyle().Foreground(colorSlate).Background(colorInputBg)
	ta.SetStyles(tas)
	ta.ShowLineNumbers = false
	ta.MaxHeight = 5

	// Disable textarea's built-in enter-to-newline so we control submit vs newline
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "new line"))

	s := spinner.New()
	s.Spinner = sigilSpinner
	s.Style = lipgloss.NewStyle()

	r, _ := buildRenderer(80)

	cfg := config.Load()

	m := &Model{
		Input:                ta,
		History:              []HistoryBlock{},
		SessionID:            session,
		Ready:                false,
		ctx:                  ctx,
		grpcClient:           gClient,
		appStore:             store,
		appCrypto:            crypto,
		Spinner:              s,
		ActiveModel:          "Detecting LLM...",
		CurrentFocus:         "vraxter",
		renderer:             r,
		UserName:             strings.ToUpper(userName),
		ActiveCardHistoryIdx: -1,
		IsolationMode:        cfg.IsolationMode,
		IsAirgapLocked:       cfg.IsAirgapLocked,
	}

	// Pre-load specialists for autocomplete
	var specList []string
	if store != nil {
		specRepo := db.NewSpecialistRepository(store)
		specs, err := specRepo.GetAllSpecialists()
		if err == nil {
			for _, sp := range specs {
				specList = append(specList, sp.Name)
			}
		}
	}
	m.AvailableSpecialists = specList

	sysChan, _ := gClient.SubscribeEvents(ctx, "tui-"+session)
	m.systemEvents = sysChan

	m.refreshModelBanner()

	if initialQuery != "" {
		m.appendHistory(BlockMarkdown, initialQuery, SenderUser)
		m.appendHistory(BlockMarkdown, vraxterIconPlaceholder+"  ", SenderVraxter)
		m.IsStreaming = true
	}

	return m
}

func (m *Model) resolveModelDisplayName(raw string) string {
	clean := strings.Trim(strings.ToLower(raw), "'\" ")
	if clean == "" || clean == "none" || clean == "offline" || clean == "detecting llm..." || clean == "not configured" {
		return raw
	}
	if m.appStore == nil || m.appCrypto == nil {
		return raw
	}
	repo := db.NewModelRepository(m.appStore, m.appCrypto)
	models, err := repo.GetAllModels()
	if err != nil {
		return raw
	}
	for _, cfg := range models {
		cleanID := strings.Trim(strings.ToLower(cfg.ID), "'\" ")
		cleanAlias := strings.Trim(strings.ToLower(cfg.Alias), "'\" ")
		cleanModel := strings.Trim(strings.ToLower(cfg.Model), "'\" ")

		if cleanID == clean || cleanAlias == clean || cleanModel == clean {
			if cfg.Alias != "" {
				return cfg.Alias
			}
			return cfg.Model
		}
	}
	return raw
}

func (m *Model) refreshModelBanner() {
	if m.appStore == nil || m.appCrypto == nil {
		return
	}
	repo := db.NewModelRepository(m.appStore, m.appCrypto)
	models, err := repo.GetActiveModels()
	if err != nil || len(models) == 0 {
		return
	}

	cleanOverride := strings.Trim(strings.ToLower(m.OverrideModelID), "'\" ")

	if m.OverrideModelID != "" {
		activeName := m.resolveModelDisplayName(m.OverrideModelID)
		m.ActiveModel = fmt.Sprintf("%s (LOCKED)", activeName)

		m.FailoverModel = "none"
		cleanActive := strings.Trim(strings.ToLower(activeName), "'\" ")
		for _, cfg := range models {
			cleanID := strings.Trim(strings.ToLower(cfg.ID), "'\" ")
			cleanAlias := strings.Trim(strings.ToLower(cfg.Alias), "'\" ")
			cleanModel := strings.Trim(strings.ToLower(cfg.Model), "'\" ")

			targetName := cfg.Model
			if cfg.Alias != "" {
				targetName = cfg.Alias
			}
			if cleanID != cleanOverride && cleanAlias != cleanOverride && cleanModel != cleanOverride &&
				cleanAlias != cleanActive && cleanModel != cleanActive && cleanID != cleanActive {
				m.FailoverModel = targetName
				break
			}
		}
	} else {
		first := models[0]
		firstName := first.Model
		if first.Alias != "" {
			firstName = first.Alias
		}
		m.ActiveModel = firstName
		m.BaseModelID = firstName

		if len(models) > 1 {
			second := models[1]
			secondName := second.Model
			if second.Alias != "" {
				secondName = second.Alias
			}
			m.FailoverModel = secondName
		} else {
			m.FailoverModel = "none"
		}
	}
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

type fetchJobsMsg struct {
	jobs []*v1.JobStatus
}

func (m *Model) fetchJobs() tea.Cmd {
	return func() tea.Msg {
		jobs, err := m.grpcClient.GetActiveJobs(m.ctx)
		if err != nil {
			return fetchJobsMsg{jobs: nil}
		}
		// Filter only running jobs
		var running []*v1.JobStatus
		for _, j := range jobs {
			if j.Status == "running" {
				running = append(running, j)
			}
		}
		return fetchJobsMsg{jobs: running}
	}
}

func (m *Model) Init() tea.Cmd {
	if m.IsStreaming {
		return tea.Batch(
			textarea.Blink,
			m.Spinner.Tick,
			m.fetchInfo(),
			m.fetchJobs(),
			m.fireExecution(m.Input.Value()),
			m.listenForSystemEvents(m.systemEvents),
		)
	}
	return tea.Batch(
		textarea.Blink,
		m.Spinner.Tick,
		m.fetchInfo(),
		m.fetchJobs(),
		m.listenForSystemEvents(m.systemEvents),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// 0. Sigil Spinner Tick: Always process immediately at top level so that modal wizards
	// or slow remote network model responses never swallow or kill the spinner tick loop.
	if tickMsg, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(tickMsg)
		if ((m.IsStreaming && !m.HasSentVraxterBanner) || m.IsPlanning) && m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
			m.Viewport.GotoBottom()
		}
		return m, cmd
	}

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
			if sw.SelectedImplementation != "" {
				cfg := config.Load()
				cfg.Implementation = sw.SelectedImplementation
				_ = cfg.SaveSettings()
			}
			done = true
		}
		if spw, ok := m.ActiveSetup.(*SpecialistWizard); ok && spw.Done {
			if spw.Result != nil {
				m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "SPECIALIST REGISTERED", fmt.Sprintf("Registered '%s'. Invoke directly with @%s", spw.Result.Name, spw.Result.Name), "SPECIALIST READY", m.width))
				if m.appStore != nil {
					specRepo := db.NewSpecialistRepository(m.appStore)
					if specs, err := specRepo.GetAllSpecialists(); err == nil {
						var specList []string
						for _, sp := range specs {
							specList = append(specList, sp.Name)
						}
						m.AvailableSpecialists = specList
					}
				}
			}
			done = true
		}

		if done {
			m.ActiveSetup = nil
			m.refreshModelBanner()
			if m.Ready {
				m.Viewport.SetContent(m.renderMarkdown(m.History))
				m.Viewport.GotoBottom()
			}
			return m, tea.Batch(setupCmd, m.fetchInfo())
		}
		return m, setupCmd
	}

	switch msg := msg.(type) {
	case fetchJobsMsg:
		m.ActiveJobs = msg.jobs
		return m, nil

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
		m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "OPERATOR PROFILE SAVED", "Workstation preferences & operator identity persisted to vault.", "SAVED", m.width))
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

	case tea.KeyPressMsg:
		// 0. Immediate priority: Help Toggle
		if msg.String() == "?" && m.Input.Value() == "" {
			m.ShowHelp = !m.ShowHelp
			return m, nil
		}

		// 1. Intercept Global Controls
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		if m.IsWaitingForApproval || m.IsWaitingForSkillApproval {
			maxChoices := 3
			if m.IsWaitingForSkillApproval || m.PendingRunSkill != "" {
				maxChoices = 2
			}
			if msg.String() == "left" {
				m.PlanActionIndex = (m.PlanActionIndex - 1 + maxChoices) % maxChoices
				if m.PlanActionIndex != 2 {
					m.ShowAmendInput = false
					m.Input.Blur()
				}
				return m, nil
			}
			if msg.String() == "right" {
				m.PlanActionIndex = (m.PlanActionIndex + 1) % maxChoices
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
		isNewlineShortcut := msg.String() == "alt+enter" || msg.String() == "ctrl+j"
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
		case "up":
			if m.Input.Line() == 0 && len(m.InputHistory) > 0 {
				if m.InputHistoryIdx > 0 {
					m.InputHistoryIdx--
					m.Input.SetValue(m.InputHistory[m.InputHistoryIdx])
					m.Input.CursorEnd()
				}
				return m, nil
			}
			if m.Input.Value() == "" {
				m.Viewport, vpCmd = m.Viewport.Update(msg)
				return m, vpCmd
			}
		case "down":
			if m.Input.Line() == m.Input.LineCount()-1 && len(m.InputHistory) > 0 && m.InputHistoryIdx < len(m.InputHistory) {
				if m.InputHistoryIdx < len(m.InputHistory)-1 {
					m.InputHistoryIdx++
					m.Input.SetValue(m.InputHistory[m.InputHistoryIdx])
					m.Input.CursorEnd()
				} else {
					m.InputHistoryIdx = len(m.InputHistory)
					m.Input.SetValue("")
				}
				return m, nil
			}
			if m.Input.Value() == "" {
				m.Viewport, vpCmd = m.Viewport.Update(msg)
				return m, vpCmd
			}
		}

		// 3. Handle Submissions (Plain Enter only)
		isPlainEnter := msg.String() == "enter"

		if isPlainEnter && !m.IsStreaming {
			if m.IsWaitingForSkillApproval {
				var cmd tea.Cmd
				if m.PlanActionIndex == 0 {
					cmd = m.handleSlashCommand("/approve")
				} else {
					cmd = m.handleSlashCommand("/reject")
				}
				if m.Ready {
					m.Viewport.SetContent(m.renderMarkdown(m.History))
					m.Viewport.GotoBottom()
				}
				return m, cmd
			}

			if m.IsWaitingForApproval && !m.ShowAmendInput {
				if m.PendingRunSkill != "" {
					switch m.PlanActionIndex {
					case 0: // RUN
						skill := m.PendingRunSkill
						m.PendingRunSkill = ""
						m.IsWaitingForApproval = false
						m.Input.Focus()
						return m, m.fireExecution(fmt.Sprintf("!RUN_SKILL %s", skill))
					case 1: // DISMISS (Done)
						m.PendingRunSkill = ""
						m.IsWaitingForApproval = false
						m.appendHistory(BlockComponent, lipgloss.NewStyle().Foreground(colorMutedGray).Render("  (Skill added to registry. Execution dismissed.)\n"))
						m.Input.Focus()
						return m, nil
					}
				}

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
			if strings.TrimSpace(val) == "" {
				return m, nil
			}

			// Clean message and reset
			messageToSend := strings.TrimSpace(val)
			m.Input.SetValue("")
			m.Input.SetHeight(1)

			// Add to Input History
			if messageToSend != "" {
				m.InputHistory = append(m.InputHistory, messageToSend)
				m.InputHistoryIdx = len(m.InputHistory)
			}

			if strings.HasPrefix(messageToSend, "/") && !m.ShowAmendInput {
				parts := splitArgs(messageToSend)
				if len(parts) > 0 && parts[0] != "/clear" && parts[0] != "/new" && parts[0] != "/reset" {
					m.appendHistory(BlockMarkdown, messageToSend, SenderUser)
				}
				cmd := m.handleSlashCommand(messageToSend)
				if m.Ready {
					m.Viewport.SetContent(m.renderMarkdown(m.History))
					m.Viewport.GotoBottom()
				}
				return m, cmd
			}

			// Intercept mention-prefixed planning command (e.g. @specialist /plan <task>)
			if strings.HasPrefix(messageToSend, "@") && !m.ShowAmendInput {
				fields := strings.Fields(messageToSend)
				if len(fields) >= 2 && fields[1] == "/plan" {
					if len(fields) < 3 {
						m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "INVALID SYNTAX", "Usage: @specialist /plan <task details>", "ARGS REQUIRED", m.width))
						if m.Ready {
							m.Viewport.SetContent(m.renderMarkdown(m.History))
							m.Viewport.GotoBottom()
						}
						return m, nil
					}
					m.appendHistory(BlockMarkdown, messageToSend, SenderUser)
					m.HasSentVraxterBanner = false
					m.IsPlanning = true
					m.IsStreaming = true
					m.ActiveCardHistoryIdx = -1
					m.ProposedPlan = ""
					m.IsWaitingForApproval = false
					m.Error = nil
					if m.Ready {
						m.Viewport.SetContent(m.renderMarkdown(m.History))
						m.Viewport.GotoBottom()
					}
					return m, tea.Batch(m.fireExecution(messageToSend), m.Spinner.Tick)
				}
			}

			if m.IsWaitingForApproval && m.ShowAmendInput {
				var prevPlan struct {
					Goal string `json:"goal"`
				}
				_ = json.Unmarshal([]byte(m.ProposedPlan), &prevPlan)

				m.updateActiveCard(m.renderPlan(m.ProposedPlan, m.width, "PLAN AMENDED") + "\n")
				m.IsWaitingForApproval = false
				m.ShowAmendInput = false
				m.IsPlanning = true
				m.IsStreaming = true
				m.HasSentVraxterBanner = false
				m.Error = nil
				m.restoreInputStyles()

				m.appendHistory(BlockMarkdown, "↻ Amend Plan: "+messageToSend, SenderUser)
				if m.Ready {
					m.Viewport.SetContent(m.renderMarkdown(m.History))
					m.Viewport.GotoBottom()
				}
				amendQuery := messageToSend
				if prevPlan.Goal != "" {
					amendQuery = fmt.Sprintf("%s (Amendment instructions: %s)", prevPlan.Goal, messageToSend)
				}
				return m, tea.Batch(m.fireExecution(fmt.Sprintf("AMEND PLAN: %s", amendQuery)), m.Spinner.Tick)
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

			return m, tea.Batch(m.fireExecution(messageToSend), m.Spinner.Tick)
		}

		// 4. Autocomplete Interception & Default Input Update
		if !m.IsStreaming {
			// A. Intercept autocomplete navigation keys
			if m.Autocomplete.IsActive {
				if msg.String() == "up" {
					if m.Autocomplete.Cursor > 0 {
						m.Autocomplete.Cursor--
					}
					return m, nil
				} else if msg.String() == "down" {
					if m.Autocomplete.Cursor < len(m.Autocomplete.Items)-1 {
						m.Autocomplete.Cursor++
					}
					return m, nil
				} else if msg.String() == "tab" || msg.String() == "enter" {
					if len(m.Autocomplete.Items) > 0 && m.Autocomplete.Cursor >= 0 && m.Autocomplete.Cursor < len(m.Autocomplete.Items) {
						selected := m.Autocomplete.Items[m.Autocomplete.Cursor]
						// Replace the prefix with the selected item
						val := m.Input.Value()
						lastSpace := strings.LastIndexAny(val, " \n\t")
						var newVal string
						if lastSpace == -1 {
							newVal = selected + " "
						} else {
							newVal = val[:lastSpace+1] + selected + " "
						}
						m.Input.SetValue(newVal)
						m.Input.SetCursorColumn(len(newVal))
						m.Autocomplete.IsActive = false
					}
					return m, nil
				} else if msg.String() == "esc" {
					m.Autocomplete.IsActive = false
					return m, nil
				}
			}

			// B. Let textarea handle normal typing
			m.Input, inputCmd = m.Input.Update(msg)

			// C. Recalculate Autocomplete state based on new input value
			val := m.Input.Value()
			lastSpace := strings.LastIndexAny(val, " \n\t")
			var lastWord string
			if lastSpace == -1 {
				lastWord = val
			} else {
				lastWord = val[lastSpace+1:]
			}

			m.Autocomplete.Prefix = lastWord
			if strings.HasPrefix(lastWord, "/") && len(lastWord) >= 1 {
				m.Autocomplete.Type = "command"
				m.Autocomplete.Items = []string{}
				for _, cmd := range slashCommandsList {
					if strings.HasPrefix(cmd, lastWord) {
						m.Autocomplete.Items = append(m.Autocomplete.Items, cmd)
					}
				}
				m.Autocomplete.IsActive = len(m.Autocomplete.Items) > 0
				if m.Autocomplete.Cursor >= len(m.Autocomplete.Items) {
					m.Autocomplete.Cursor = 0
				}
			} else if strings.HasPrefix(lastWord, "@") && len(lastWord) >= 1 {
				m.Autocomplete.Type = "specialist"
				m.Autocomplete.Items = []string{}
				search := strings.TrimPrefix(lastWord, "@")
				for _, sp := range m.AvailableSpecialists {
					if strings.HasPrefix(strings.ToLower(sp), strings.ToLower(search)) {
						m.Autocomplete.Items = append(m.Autocomplete.Items, "@"+sp)
					}
				}
				m.Autocomplete.IsActive = len(m.Autocomplete.Items) > 0
				if m.Autocomplete.Cursor >= len(m.Autocomplete.Items) {
					m.Autocomplete.Cursor = 0
				}
			} else {
				m.Autocomplete.IsActive = false
			}

			// Auto-grow: recalculate height after every keystroke to account for word-wrap.
			availableWidth := m.width - 6
			if availableWidth < 10 {
				availableWidth = 10 // Safety fallback
			}

			visualLines := 0
			for _, hardLine := range strings.Split(val, "\n") {
				if len(hardLine) == 0 {
					visualLines++
					continue
				}
				// Calculate how many visual lines this hard line takes up.
				// This robustly handles both soft-wrapping and long unbroken words.
				visualLines += (len(hardLine) / availableWidth)
				if len(hardLine)%availableWidth != 0 {
					visualLines++
				}
			}

			if visualLines > m.Input.MaxHeight {
				visualLines = m.Input.MaxHeight
			}
			if visualLines < 1 {
				visualLines = 1
			}
			m.Input.SetHeight(visualLines)

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
			m.Viewport = viewport.New()
			m.Viewport.SetWidth(msg.Width - 4)
			m.Viewport.SetHeight(msg.Height - verticalMarginHeight)
			m.Ready = true
		} else {
			m.Viewport.SetWidth(msg.Width - 4)
			m.Viewport.SetHeight(msg.Height - verticalMarginHeight)
		}

		// 3. Update Content AFTER renderer is ready
		m.Viewport.SetContent(m.renderMarkdown(m.History))
		m.Input.SetWidth(msg.Width - 6)

	case tea.MouseMotionMsg:
		return m, nil

	case tea.MouseMsg:
		mMsg := msg.Mouse()
		// We check if the mouse is within the viewport's vertical bounds
		inBounds := mMsg.Y >= m.viewportY && mMsg.Y < m.viewportY+m.Viewport.Height()

		if inBounds {
			// Capture scroll wheel events and clicks
			if mMsg.Button == tea.MouseWheelUp || mMsg.Button == tea.MouseWheelDown || mMsg.Button == tea.MouseLeft {
				m.Viewport, vpCmd = m.Viewport.Update(msg)
				return m, vpCmd
			}
		}

		// Button Hit Detection
		if m.IsWaitingForApproval && mMsg.Button == tea.MouseLeft {
			// Footer buttons are typically in the last 10 rows
			// We iterate through button X/Y bounds
			// Approximated for now based on renderDecisionBridge
			footerHeight := 5
			if m.ShowAmendInput {
				footerHeight = 8
			}
			buttonRow := m.height - footerHeight - 1
			if mMsg.Y >= buttonRow && mMsg.Y <= buttonRow+2 {
				// 3 buttons across footerWidth
				btnWidth := (m.width - 4) / 3
				if mMsg.X >= 2 && mMsg.X < 2+btnWidth {
					m.PlanActionIndex = 0
					return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				} else if mMsg.X >= 2+btnWidth && mMsg.X < 2+(btnWidth*2) {
					m.PlanActionIndex = 1
					return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				} else if mMsg.X >= 2+(btnWidth*2) && mMsg.X < m.width-2 {
					m.PlanActionIndex = 2
					m.ShowAmendInput = true
					m.Input.Focus()
					return m, nil
				}
			}
		}

		return m, nil

	case InfoMsg:
		m.BaseModelID = m.resolveModelDisplayName(msg.ModelID)
		m.refreshModelBanner()

	case HistoryResultMsg:
		if msg.Err != nil {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "HISTORY LOOKUP FAILED", msg.Err.Error(), "ERROR", m.width))
		} else if len(msg.Conversations) == 0 {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("started", "CONVERSATION HISTORY", "No saved sessions found in local vault.", "0 SESSIONS", m.width))
		} else {
			m.lastHistoryResults = msg.Conversations

			hdrStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
			idxStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
			titleColStyle := lipgloss.NewStyle().Foreground(colorIvory)
			countStyle := lipgloss.NewStyle().Foreground(colorLedEmerald)
			timeStyle := lipgloss.NewStyle().Foreground(colorAsh)
			divStyle := lipgloss.NewStyle().Foreground(colorTitaniumMid)

			var sb strings.Builder
			sb.WriteString(hdrStyle.Render(fmt.Sprintf("%-5s %-44s %-10s %s", "IDX", "SESSION TITLE", "MESSAGES", "LAST ACTIVE")) + "\n")
			sb.WriteString(divStyle.Render(strings.Repeat("─", 74)) + "\n")
			for i, c := range msg.Conversations {
				title := c.Title
				if len(title) > 42 {
					title = title[:42] + "…"
				}
				updated := c.UpdatedAt
				if t, err := time.Parse(time.RFC3339, c.UpdatedAt); err == nil {
					updated = t.Format("Jan 02 15:04")
				}
				row := idxStyle.Render(fmt.Sprintf("#%-4d", i+1)) + " " +
					titleColStyle.Render(fmt.Sprintf("%-44s", title)) + " " +
					countStyle.Render(fmt.Sprintf("%-10d", c.MessageCount)) + " " +
					timeStyle.Render(updated)
				sb.WriteString(row + "\n")
			}
			sb.WriteString("\n" + timeStyle.Render("Tip: Type ") + idxStyle.Render("/load <#>") + timeStyle.Render(" to replay a conversation (e.g. /load 1)."))

			m.appendHistory(BlockComponent, m.renderCmdBlock("/history", sb.String()))
		}
		if m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
			m.Viewport.GotoBottom()
		}
		return m, nil

	case SystemEventMsgWrapper:
		if msg.Event == nil {
			return m, nil
		}

		updatedHistory := false
		switch msg.Event.Type {
		case v1.SystemEvent_JOB_STARTED:
			if strings.HasPrefix(msg.Event.Title, "Architecture Planner") {
				m.IsPlanning = true
			}
			notif := m.renderNotification("⚙", msg.Event.Title+":", fmt.Sprintf("Background daemon initialized (ID: %s)", msg.Event.JobId), "DAEMON ACTIVE")
			m.appendHistory(BlockComponent, notif)
			updatedHistory = true
		case v1.SystemEvent_JOB_COMPLETED:
			isPlanner := strings.HasPrefix(msg.Event.Title, "Architecture Planner")
			if isPlanner {
				m.IsPlanning = false
			}
			notif := m.renderNotification("✓", msg.Event.Title+":", "Execution finished with zero perimeter egress.", "STRICT_LOCAL VERIFIED")
			if isPlanner && m.ActiveCardHistoryIdx >= 0 && m.ActiveCardHistoryIdx < len(m.History) {
				m.insertHistoryAt(m.ActiveCardHistoryIdx, BlockComponent, notif)
				m.ActiveCardHistoryIdx++
			} else {
				m.appendHistory(BlockComponent, notif)
			}
			if !isPlanner && strings.TrimSpace(msg.Event.Payload) != "" && strings.TrimSpace(msg.Event.Payload) != "Plan successfully generated and pushed to UI." {
				m.appendHistory(BlockMarkdown, strings.TrimSpace(msg.Event.Payload), SenderVraxter)
			}
			updatedHistory = true
		case v1.SystemEvent_JOB_FAILED:
			m.IsPlanning = false
			m.IsStreaming = false
			notif := m.renderNotification("✕", msg.Event.Title+":", msg.Event.Payload, "EXECUTION ERROR")
			m.appendHistory(BlockComponent, notif)
			updatedHistory = true
		case v1.SystemEvent_SWARM_PROGRESS:
			notif := m.renderNotification("⚡", "Swarm Telemetry:", msg.Event.Payload, "PHASE ACTIVE")
			m.appendHistory(BlockComponent, notif)
			updatedHistory = true
		case v1.SystemEvent_PLAN_PROPOSED:
			m.IsPlanning = false
			m.IsStreaming = false
			m.IsWaitingForApproval = true
			m.ProposedPlan = msg.Event.Payload
			m.PlanActionIndex = 0 // Default to PROCEED
			m.ShowAmendInput = false
			m.Input.Blur()
			m.ActiveCardHistoryIdx = len(m.History)
			m.appendHistory(BlockComponent, m.renderPlan(msg.Event.Payload, m.width, "")+"\n")
			updatedHistory = true
		}

		if updatedHistory && m.Ready {
			m.Viewport.SetContent(m.renderMarkdown(m.History))
			m.Viewport.GotoBottom()
		}

		if msg.Event.Type == v1.SystemEvent_JOB_STARTED || msg.Event.Type == v1.SystemEvent_JOB_COMPLETED || msg.Event.Type == v1.SystemEvent_JOB_FAILED {
			return m, tea.Batch(m.listenForSystemEvents(m.systemEvents), m.fetchJobs())
		}

		return m, m.listenForSystemEvents(m.systemEvents)

	case EventMsgWrapper:
		// Real-time Model Sync & Fallback Tracking
		if msg.Event.ActiveModelID != "" {
			drivingModel := m.resolveModelDisplayName(msg.Event.ActiveModelID)
			cleanDriving := strings.Trim(strings.ToLower(drivingModel), "'\" ")

			if m.OverrideModelID != "" {
				lockedDisplay := m.resolveModelDisplayName(m.OverrideModelID)
				cleanOverride := strings.Trim(strings.ToLower(m.OverrideModelID), "'\" ")
				cleanDisplay := strings.Trim(strings.ToLower(lockedDisplay), "'\" ")

				if cleanDriving != cleanOverride && cleanDriving != cleanDisplay &&
					!strings.Contains(cleanOverride, cleanDriving) && !strings.Contains(cleanDisplay, cleanDriving) {
					// Fallback occurred away from the locked override model
					m.ActiveModel = fmt.Sprintf("%s (FALLBACK)", drivingModel)
					m.FailoverModel = fmt.Sprintf("failed: %s", lockedDisplay)
				} else {
					m.ActiveModel = fmt.Sprintf("%s (LOCKED)", lockedDisplay)
				}
			} else if m.BaseModelID != "" {
				baseDisplay := m.resolveModelDisplayName(m.BaseModelID)
				cleanBase := strings.Trim(strings.ToLower(baseDisplay), "'\" ")

				if cleanDriving != cleanBase && !strings.Contains(cleanBase, cleanDriving) && !strings.Contains(cleanDriving, cleanBase) {
					// Fallback occurred away from the primary default model
					m.ActiveModel = fmt.Sprintf("%s (FALLBACK)", drivingModel)
				} else {
					m.ActiveModel = baseDisplay
				}
			} else {
				m.ActiveModel = drivingModel
			}
		}

		switch msg.Event.Type {
		case llm.EventTypeToken:
			content := msg.Event.Content

			// Intercept System Telemetry Notifications (Do NOT render inside message body box)
			if strings.Contains(content, "Sub-Agent Swarm Spawned") || strings.Contains(content, "Delegating task to") {
				return m, nil
			}
			if strings.Contains(content, "Control returned to Vraxter") {
				return m, nil
			}
			if strings.Contains(content, "Spawned Architecture Planner Daemon") || strings.Contains(content, "Spawned Plan Execution Daemon") {
				return m, nil
			}
			if strings.Contains(content, "Skill Execution Approved") {
				cleanMsg := strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
				cleanMsg = strings.ReplaceAll(cleanMsg, "**", "")
				cleanMsg = strings.ReplaceAll(cleanMsg, "`", "")
				cleanMsg = strings.TrimPrefix(cleanMsg, "✅")
				cleanMsg = strings.TrimSpace(cleanMsg)
				m.appendHistory(BlockComponent, m.renderNotification("✓", "Skill Approved:", cleanMsg, "APPROVED"))
				return m, nil
			}
			if strings.HasPrefix(strings.TrimSpace(content), "▶️ **Running skill:**") {
				cleanMsg := strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
				cleanMsg = strings.ReplaceAll(cleanMsg, "**", "")
				cleanMsg = strings.ReplaceAll(cleanMsg, "`", "")
				cleanMsg = strings.TrimPrefix(cleanMsg, "▶️")
				cleanMsg = strings.TrimSpace(cleanMsg)
				m.appendHistory(BlockComponent, m.renderNotification("◆", "Running Skill:", cleanMsg, "ACTIVE"))
				return m, nil
			}
			if strings.Contains(content, "Sub-Agent Specialist") && strings.Contains(content, "created") {
				cleanMsg := strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
				cleanMsg = strings.TrimPrefix(cleanMsg, "✅")
				cleanMsg = strings.TrimSpace(cleanMsg)
				m.appendHistory(BlockComponent, m.renderNotification("✓", "Specialist Created:", cleanMsg, "REGISTERED"))
				return m, nil
			}
			if strings.Contains(content, "created and installed successfully") {
				cleanMsg := strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
				cleanMsg = strings.TrimPrefix(cleanMsg, "✅")
				cleanMsg = strings.TrimSpace(cleanMsg)
				m.appendHistory(BlockComponent, m.renderNotification("✓", "Skill Installed:", cleanMsg, "INSTALLED"))
				return m, nil
			}

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

			// Intercept PROMPT_RUN control signal
			if strings.Contains(content, "[PROMPT_RUN:") {
				parts := strings.Split(content, ":")
				if len(parts) >= 2 {
					skillID := strings.TrimSuffix(parts[1], "]")
					m.PendingRunSkill = skillID
					m.IsWaitingForApproval = true
					m.PlanActionIndex = 0 // Default to RUN
					m.ShowAmendInput = false
					m.Input.Blur()
					content = strings.ReplaceAll(content, "[PROMPT_RUN:"+skillID+"]", "")
				}
			}

			if content != "" {
				if m.isThinking {
					m.appendHistory(BlockThought, content)
				} else {
					sender := SenderVraxter
					if m.CurrentFocus == "specialist" {
						sender = SenderSpecialist
					}
					m.appendHistory(BlockMarkdown, content, sender)
				}
			}
		case llm.EventTypeStatus:
			m.StatusMessage = msg.Event.Content
			cmds = append(cmds, func() tea.Msg {
				time.Sleep(4 * time.Second)
				return ClearStatusMsg{}
			})
		case llm.EventTypeSkillCall:
			label := msg.Event.Content
			if label == "vraxter-return-control" {
				m.CurrentFocus = "vraxter"     // ◈ Focus shifts for THIS turn
				m.HasSentVraxterBanner = false // Allow supervisor banner to spawn
				m.ActiveSpecialistID = ""      // Clear latched specialist
				m.SpecialistBanner = ""
				m.appendHistory(BlockComponent, m.renderNotification("◈", "Supervisor Resumed:", "Specialist boundary reached. Control returned to Vraxter", "VRAXTER"))
			} else {
				m.appendHistory(BlockComponent, m.renderNotification("◆", "Running Tool:", label, "ACTIVE"))
			}
		case llm.EventTypePlanProposal:
			m.IsPlanning = false
			m.IsWaitingForApproval = true
			m.ProposedPlan = msg.Event.Content
			m.PlanActionIndex = 0 // Default to PROCEED
			m.ShowAmendInput = false
			m.Input.Blur()
			m.ActiveCardHistoryIdx = len(m.History)
			m.appendHistory(BlockComponent, m.renderPlan(msg.Event.Content, m.width, "")+"\n")
		case llm.EventTypeSkillApprovalRequest:
			m.IsWaitingForSkillApproval = true
			m.PendingSkillPayload = msg.Event.Content
			m.PlanActionIndex = 0 // Default to PROCEED
			m.Input.Blur()
			m.ActiveCardHistoryIdx = len(m.History)
			m.appendHistory(BlockComponent, m.renderSkillApproval(msg.Event.SkillID, msg.Event.Content, m.width, "")+"\n")
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

				if id == "" && name == "" {
					// Control returned to Vraxter
					if m.ActiveSpecialistID != "" || m.CurrentFocus == "specialist" {
						m.ActiveSpecialistID = ""
						m.SpecialistBanner = ""
						m.CurrentFocus = "vraxter"
						m.HasSentVraxterBanner = false
						m.appendHistory(BlockComponent, m.renderNotification("◈", "Supervisor Resumed:", "Control returned to Vraxter", "VRAXTER"))
					}
				} else {
					// Delegated to specialist
					if m.ActiveSpecialistID != id {
						m.ActiveSpecialistID = id
						m.SpecialistBanner = strings.ToUpper(name)
						m.CurrentFocus = "specialist"
						m.HasSentVraxterBanner = false
						m.appendHistory(BlockComponent, m.renderNotification("⚡", "Swarm Delegated:", "@"+name, "SWARM ACTIVE"))
					}
				}

				if actualContent != "" {
					m.appendHistory(BlockMarkdown, actualContent)
				}
			}
		case llm.EventTypeError:
			m.Error = msg.Event.Err
			m.IsPlanning = false
			m.IsStreaming = false
			errNotice := lipgloss.NewStyle().
				Foreground(colorLedCrimson).
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

func (m *Model) insertHistoryAt(idx int, t BlockType, content string, sender ...SenderType) {
	s := SenderSystem
	if len(sender) > 0 {
		s = sender[0]
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.History) {
		m.appendHistory(t, content, sender...)
		return
	}
	block := HistoryBlock{Type: t, Content: content, Sender: s}
	newHistory := make([]HistoryBlock, 0, len(m.History)+1)
	newHistory = append(newHistory, m.History[:idx]...)
	newHistory = append(newHistory, block)
	newHistory = append(newHistory, m.History[idx:]...)
	m.History = newHistory
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

func (m *Model) renderHelpContent() string {
	catStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
	argStyle := lipgloss.NewStyle().Foreground(colorCopper)
	descStyle := lipgloss.NewStyle().Foreground(colorStone)
	divStyle := lipgloss.NewStyle().Foreground(colorTitaniumMid)

	type helpRow struct {
		cmd  string
		args string
		desc string
	}
	renderSection := func(title string, rows []helpRow) string {
		var sb strings.Builder
		sb.WriteString(catStyle.Render("◆ "+title) + "\n")
		sb.WriteString(divStyle.Render(strings.Repeat("─", 72)) + "\n")
		for _, r := range rows {
			left := cmdStyle.Render(r.cmd)
			if r.args != "" {
				left += " " + argStyle.Render(r.args)
			}
			pad := 26 - lipgloss.Width(left)
			if pad < 2 {
				pad = 2
			}
			sb.WriteString("  " + left + strings.Repeat(" ", pad) + descStyle.Render(r.desc) + "\n")
		}
		return sb.String()
	}

	var out strings.Builder
	out.WriteString(renderSection("SESSION & ROUTING", []helpRow{
		{"/activate", "<id>", "Manually switch and lock the active model for this session"},
		{"/restore, /auto", "", "Restore automatic use-case based model routing"},
		{"/history", "", "Browse past conversation sessions in the local vault"},
		{"/load", "<#>", "Replay a past conversation by its index from /history"},
		{"/session", "<id>", "Switch context to a different session ID"},
		{"/new, /reset", "", "Generate a fresh session ID and clear history"},
		{"/clear", "", "Clear the chat viewport"},
	}) + "\n")

	out.WriteString(renderSection("AUTONOMOUS EXECUTION & SWARM", []helpRow{
		{"/plan", "<task>", "Break down a complex task into a multi-phase .vrx plan"},
		{"/jobs", "", "List all background daemon jobs currently running"},
		{"/skills", "[args]", "Manage & synthesize skills (/skills add <name> <spec>)"},
		{"/specialists", "[args]", "Manage domain specialists (or delegate via @<name>)"},
	}) + "\n")

	out.WriteString(renderSection("WORKSTATION & CONFIGURATION", []helpRow{
		{"/start", "", "Unified startup wizard (Profile ➔ Providers ➔ Models)"},
		{"/airgap, /isolation", "", "Inspect zero-egress perimeter defense status & telemetry"},
		{"/factory-reset", "[--confirm]", "Restore Vraxter completely to clean factory defaults"},
		{"/providers", "[args]", "Manage AI providers (/providers setup for wizard)"},
		{"/models", "[args]", "Manage models linked to providers (/models setup)"},
		{"/user", "[args]", "Manage operator profile (/user setup for wizard)"},
		{"/help, /?", "", "Show this command reference"},
		{"/quit, /exit", "", "Exit Vraxter"},
	}))

	return strings.TrimRight(out.String(), "\n")
}

func (m *Model) handleSlashCommand(cmd string) tea.Cmd {
	parts := splitArgs(cmd)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "/jobs":
		if len(m.ActiveJobs) == 0 {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("started", "BACKGROUND DAEMON JOBS", "No background jobs are currently running.", "0 ACTIVE", m.width))
			return nil
		}
		hdrStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
		idStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
		titleColStyle := lipgloss.NewStyle().Foreground(colorIvory)
		statusColStyle := lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true)
		timeStyle := lipgloss.NewStyle().Foreground(colorAsh)
		divStyle := lipgloss.NewStyle().Foreground(colorTitaniumMid)

		var sb strings.Builder
		sb.WriteString(hdrStyle.Render(fmt.Sprintf("%-14s %-36s %-12s %s", "JOB ID", "TASK TITLE", "STATUS", "STARTED")) + "\n")
		sb.WriteString(divStyle.Render(strings.Repeat("─", 74)) + "\n")
		for _, j := range m.ActiveJobs {
			started := j.StartTime
			if t, err := time.Parse(time.RFC3339, j.StartTime); err == nil {
				started = t.Format("15:04:05")
			}
			row := idStyle.Render(fmt.Sprintf("%-14s", j.Id)) + " " +
				titleColStyle.Render(fmt.Sprintf("%-36s", j.Title)) + " " +
				statusColStyle.Render(fmt.Sprintf("%-12s", j.Status)) + " " +
				timeStyle.Render(started)
			sb.WriteString(row + "\n")
		}
		m.appendHistory(BlockComponent, m.renderCmdBlock(cmd, strings.TrimRight(sb.String(), "\n")))
		return nil
	case "/airgap", "/isolation":
		lockStatus := "IMMUTABLE (Enforced via --airgap / VRAXTER_AIRGAP)"
		if !m.IsAirgapLocked {
			lockStatus = "DYNAMIC (Process Settings)"
		}
		modeStr := strings.ToUpper(m.IsolationMode)
		if modeStr == "" {
			modeStr = "AIRGAP (DEFAULT)"
		}

		cardWidth := m.width - 6
		if cardWidth < 50 {
			cardWidth = 50
		}

		hdrStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
		lblStyle := lipgloss.NewStyle().Foreground(colorAsh)
		valStyle := lipgloss.NewStyle().Foreground(colorIvory).Bold(true)
		emStyle := lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true)

		var sb strings.Builder
		sb.WriteString(hdrStyle.Render("🛡️ VRAXTER PERIMETER DEFENSE TELEMETRY") + "\n")
		sb.WriteString(lipgloss.NewStyle().Foreground(colorCopperLine).Render(strings.Repeat("─", cardWidth-4)) + "\n")
		sb.WriteString(fmt.Sprintf("%-24s %s\n", lblStyle.Render("ACTIVE MODE:"), emStyle.Render(modeStr)))
		sb.WriteString(fmt.Sprintf("%-24s %s\n", lblStyle.Render("LOCK STATUS:"), valStyle.Render(lockStatus)))
		sb.WriteString(fmt.Sprintf("%-24s %s\n", lblStyle.Render("PERMITTED INTERFACES:"), valStyle.Render("Loopback (127.0.0.1, ::1) only")))
		sb.WriteString(fmt.Sprintf("%-24s %s\n", lblStyle.Render("DNS RESOLVER:"), emStyle.Render("0 External Queries (In-Memory Rejection Active)")))
		sb.WriteString(fmt.Sprintf("%-24s %s\n", lblStyle.Render("SOCKET DIALER:"), emStyle.Render("AirgapDialer (Zero-Egress syscall.EPERM)")))
		sb.WriteString(fmt.Sprintf("%-24s %s\n", lblStyle.Render("CLOUD PROVIDERS:"), lipgloss.NewStyle().Foreground(colorLedCrimson).Bold(true).Render("DEACTIVATED")))

		m.appendHistory(BlockComponent, m.renderCmdBlock(cmd, strings.TrimRight(sb.String(), "\n")))
		return nil
	case "/clear":
		m.History = []HistoryBlock{}
		return nil
	case "/session":
		if len(parts) > 1 {
			m.SessionID = parts[1]
			m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "SESSION CONTEXT SWITCHED", fmt.Sprintf("Active session bound to '%s'", m.SessionID), "ACTIVE", m.width))
		} else {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "INVALID SYNTAX", "Usage: /session <session_id>", "ARGS REQUIRED", m.width))
		}
		return nil
	case "/help", "/?":
		m.appendHistory(BlockComponent, m.renderCmdBlock(cmd, m.renderHelpContent()))
		return nil
	case "/activate":
		if len(parts) < 2 {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "INVALID SYNTAX", "Usage: /activate <model_id_or_alias>", "ARGS REQUIRED", m.width))
			return nil
		}
		rawTarget := strings.TrimSpace(strings.Join(parts[1:], " "))
		rawTarget = strings.Trim(rawTarget, "'\"")
		m.OverrideModelID = rawTarget
		m.refreshModelBanner()
		displayName := m.resolveModelDisplayName(rawTarget)
		m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "MODEL OVERRIDE LOCKED", fmt.Sprintf("Pinned active model to '%s'. Automatic routing bypassed.", displayName), "LOCKED", m.width))
		return nil
	case "/restore", "/auto":
		m.OverrideModelID = ""
		m.refreshModelBanner()
		m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "AUTOMATIC ROUTING RESTORED", "Model selection returned to use-case driven routing.", "AUTO ROUTE", m.width))
		return m.fetchInfo()
	case "/history":
		return func() tea.Msg {
			convs, err := m.grpcClient.ListHistory(m.ctx, 20)
			return HistoryResultMsg{Conversations: convs, Err: err}
		}
	case "/load":
		if len(parts) < 2 {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "INVALID SYNTAX", "Usage: /load <#> — Use the row number from /history", "ARGS REQUIRED", m.width))
			return nil
		}

		// Try parsing as a numeric index first
		var targetID string
		if idx, err := strconv.Atoi(parts[1]); err == nil {
			if idx < 1 || idx > len(m.lastHistoryResults) {
				if len(m.lastHistoryResults) == 0 {
					m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "HISTORY NOT LOADED", "Run /history first to list available conversations.", "NO INDEX", m.width))
				} else {
					m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "INDEX OUT OF BOUNDS", fmt.Sprintf("Choose a conversation index between 1 and %d.", len(m.lastHistoryResults)), "INVALID IDX", m.width))
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
		m.appendHistory(BlockComponent, m.renderExecutionBanner("started", "LOADING CONVERSATION", fmt.Sprintf("Replaying transcript for session '%s'...", displayLabel), "SYNCING", m.width))

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
	case "/skills":
		if len(parts) >= 3 && parts[1] == "add" {
			name := parts[2]
			desc := strings.Join(parts[3:], " ")
			m.IsStreaming = true
			m.appendHistory(BlockComponent, m.renderExecutionBanner("swarm", "SKILL SYNTHESIS DISPATCHED", fmt.Sprintf("Triggering @vraxter-coder for skill '%s'", name), "DISPATCHED", m.width))

			// We send a direct tool call payload to the server.
			// The server's ProcessRawIntent will see the [VRAX_TOOL] prefix and route it to DispatchToolPayload.
			payload := fmt.Sprintf(`[VRAX_TOOL] {"skill_id": "vraxter-coder", "params": {"name": "%s", "description": "%s", "spec": "%s", "prompt_run": true}}`, name, desc, desc)
			return m.fireExecution(payload)
		}

		// Sub-command proxy: forward everything else to the CLI
		execArgs := append([]string{"skills"}, parts[1:]...)
		cmdInput := cmd

		return func() tea.Msg {
			goCmd := exec.Command(os.Args[0], execArgs...)
			goCmd.Env = append(os.Environ(), "VRAXTER_INTERNAL_SESSION=true")
			out, _ := goCmd.CombinedOutput()

			cleanOut := strings.ReplaceAll(string(out), "Type a command for Vraxter... (Ctrl+C to quit)", "")
			cleanOut = stripANSI(strings.TrimSpace(cleanOut))

			return cmdOutputMsg{input: cmdInput, output: cleanOut}
		}
	case "/plan":
		if len(parts) < 2 {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "INVALID SYNTAX", "Usage: /plan <task details>", "ARGS REQUIRED", m.width))
			return nil
		}
		m.HasSentVraxterBanner = false
		m.IsPlanning = true
		m.IsStreaming = true
		m.ActiveCardHistoryIdx = -1
		m.ProposedPlan = ""
		m.IsWaitingForApproval = false
		return tea.Batch(m.fireExecution(cmd), m.Spinner.Tick)
	case "/new", "/reset":
		m.SessionID = fmt.Sprintf("new-%d-%x", time.Now().Unix(), md5.Sum([]byte(uuid.New().String())))
		m.History = []HistoryBlock{}
		m.ActiveCardHistoryIdx = -1
		m.IsPlanning = false
		m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "SESSION MEMORY REFRESHED", fmt.Sprintf("Initialized clean context (%s)", m.SessionID), "NEW SESSION", m.width))
		return nil
	case "/accept":
		if !m.IsWaitingForApproval {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "NO ACTIVE PLAN", "No architectural plan is currently awaiting approval.", "IDLE", m.width))
			return nil
		}

		// Update History in-place to show Acceptance INSIDE the box
		m.updateActiveCard(m.renderPlan(m.ProposedPlan, m.width, "PLAN ACCEPTED · EXECUTING") + "\n")

		m.IsWaitingForApproval = false
		m.ShowAmendInput = false
		m.IsStreaming = true
		m.restoreInputStyles()

		return tea.Batch(m.fireExecution("!EXECUTE_PLAN "+m.ProposedPlan), m.Spinner.Tick)
	case "/reject", "/deny":
		if m.IsWaitingForSkillApproval {
			var call types.ToolCall
			skillID := ""
			if err := json.Unmarshal([]byte(m.PendingSkillPayload), &call); err == nil {
				skillID = call.SkillID
			}
			m.updateActiveCard(m.renderSkillApproval(skillID, m.PendingSkillPayload, m.width, "SKILL REJECTED") + "\n")

			m.IsWaitingForSkillApproval = false
			m.IsStreaming = true
			m.restoreInputStyles()

			return tea.Batch(m.fireExecution("!REJECT_SKILL "+m.PendingSkillPayload), m.Spinner.Tick)
		}

		if !m.IsWaitingForApproval {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "NOTHING TO REJECT", "No pending plan or skill execution is awaiting approval.", "IDLE", m.width))
			return nil
		}

		// Otherwise handle Plan Rejection
		m.updateActiveCard(m.renderPlan(m.ProposedPlan, m.width, "PLAN REJECTED") + "\n")

		m.IsWaitingForApproval = false
		m.ShowAmendInput = false
		m.ProposedPlan = ""
		m.restoreInputStyles()

		return nil
	case "/approve":
		if !m.IsWaitingForSkillApproval {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "NO PENDING SKILL", "No skill execution is currently awaiting approval.", "IDLE", m.width))
			return nil
		}

		var call types.ToolCall
		skillID := ""
		if err := json.Unmarshal([]byte(m.PendingSkillPayload), &call); err == nil {
			skillID = call.SkillID
		}
		m.updateActiveCard(m.renderSkillApproval(skillID, m.PendingSkillPayload, m.width, "SKILL APPROVED") + "\n")

		m.IsWaitingForSkillApproval = false
		m.IsStreaming = true
		m.restoreInputStyles()

		return tea.Batch(m.fireExecution("!APPROVE_SKILL "+m.PendingSkillPayload), m.Spinner.Tick)
	case "/quit", "/exit":
		m.appendHistory(BlockComponent, m.renderExecutionBanner("started", "TERMINAL EXIT", "Press Ctrl+C to close the Vraxter workstation.", "STANDBY", m.width))
		return nil
	case "/factory-reset":
		if len(parts) < 2 || parts[1] != "--confirm" {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "FACTORY RESET WARNING", "This will wipe all models, providers, specialists, and keys.\nAn automatic backup will be created in ~/.config/vraxter.backup-<timestamp>.\nType '/factory-reset --confirm' to proceed.", "ACTION REQUIRED", m.width))
			return nil
		}

		if m.appStore != nil {
			m.appStore.Close()
			m.appStore = nil
		}

		backupPath, err := config.PerformFactoryReset(true)
		if err != nil {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "RESET FAILED", err.Error(), "ERR", m.width))
			return nil
		}

		cfg := config.Load()
		newStore, err := db.NewStore(cfg.DBPath)
		if err != nil {
			m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "DB INIT FAILED", err.Error(), "ERR", m.width))
			return nil
		}
		m.appStore = newStore

		newCrypto, err := security.NewCryptoService(cfg.KeyPath)
		if err == nil {
			m.appCrypto = newCrypto
		}

		pRepo := db.NewProviderRepository(m.appStore, m.appCrypto)
		mRepo := db.NewModelRepository(m.appStore, m.appCrypto)
		pMgr := services.NewProviderManager(pRepo, "ask", nil)
		mMgr := services.NewModelManager(mRepo, pRepo)

		m.History = []HistoryBlock{}
		m.SessionID = fmt.Sprintf("fresh-%d", time.Now().Unix())
		m.ActiveModel = "Not configured"
		m.FailoverModel = "none"
		m.OverrideModelID = ""
		m.BaseModelID = ""

		m.appendHistory(BlockComponent, m.renderExecutionBanner("confirmed", "FACTORY RESET COMPLETE", fmt.Sprintf("Vraxter restored to factory defaults.\nBackup saved to: %s\nLaunching setup wizard...", backupPath), "RESET COMPLETE", m.width))
		m.ActiveSetup = NewStartupWizard(pMgr, mMgr)
		return nil
	case "/start":
		pRepo := db.NewProviderRepository(m.appStore, m.appCrypto)
		mRepo := db.NewModelRepository(m.appStore, m.appCrypto)
		pMgr := services.NewProviderManager(pRepo, "ask", nil)
		mMgr := services.NewModelManager(mRepo, pRepo)
		m.ActiveSetup = NewStartupWizard(pMgr, mMgr)
		return nil
	case "/providers", "/models", "/specialists", "/user":
		if cmd == "/user setup" && len(parts) == 2 && parts[1] == "setup" {
			m.ActiveSetup = NewUserWizardModel()
			return nil
		}
		if cmd == "/providers setup" && len(parts) == 2 && parts[1] == "setup" {
			repo := db.NewProviderRepository(m.appStore, m.appCrypto)
			mgr := services.NewProviderManager(repo, "ask", nil)
			m.ActiveSetup = NewProviderWizard(mgr)
			return nil
		}
		if cmd == "/models setup" && len(parts) == 2 && parts[1] == "setup" {
			pRepo := db.NewProviderRepository(m.appStore, m.appCrypto)
			mRepo := db.NewModelRepository(m.appStore, m.appCrypto)
			pMgr := services.NewProviderManager(pRepo, "ask", nil)
			mMgr := services.NewModelManager(mRepo, pRepo)
			m.ActiveSetup = NewModelWizard(pMgr, mMgr)
			return nil
		}
		if parts[0] == "/specialists" && len(parts) >= 2 && (parts[1] == "create" || parts[1] == "setup" || parts[1] == "add" || parts[1] == "new") {
			specRepo := db.NewSpecialistRepository(m.appStore)
			modelsRepo := db.NewModelRepository(m.appStore, m.appCrypto)
			m.ActiveSetup = NewSpecialistWizard(specRepo, modelsRepo)
			return nil
		}

		// Intercept /providers <id> discover for native rich rendering
		if parts[0] == "/providers" && len(parts) == 3 && parts[2] == "discover" {
			pID := parts[1]
			return func() tea.Msg {
				repo := db.NewProviderRepository(m.appStore, m.appCrypto)
				mgr := services.NewProviderManager(repo, "ask", nil)
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
		m.appendHistory(BlockComponent, m.renderExecutionBanner("failed", "UNKNOWN COMMAND", fmt.Sprintf("Unrecognized command '%s'. Type /help to inspect available commands.", parts[0]), "SYNTAX ERR", m.width))
		return nil
	}
}

func (m *Model) renderDiscoveryRich(pID string, models []interfaces.ModelMetadata) string {
	width := m.width - 6

	styleGold := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	styleID := lipgloss.NewStyle().Foreground(colorIvory)
	styleMuted := lipgloss.NewStyle().Foreground(colorAsh)
	styleHeader := lipgloss.NewStyle().Foreground(colorStone)
	styleCaps := lipgloss.NewStyle().Foreground(colorAsh)
	styleDivider := lipgloss.NewStyle().Foreground(colorTitaniumLight)
	bracket := lipgloss.NewStyle().Foreground(colorSlate).Render
	ctxVal := lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true)

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

	// Group models by type
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

	// Title
	sb.WriteString(styleGold.Render(
		fmt.Sprintf("◆ REMOTE MODELS · %s", strings.ToUpper(pID)),
	) + "\n\n")

	// Column headers
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

		// Group header
		sb.WriteString("\n" + styleMuted.Render(fmt.Sprintf("── %s", gTitle)) + "\n")

		for _, mod := range ms {
			badge := ctxBadge(mod.ContextWindow)

			// ID - manual padding to align column
			idRendered := styleID.Render(mod.ID)
			idPad := idW - len(mod.ID) // use raw string length for pad
			if idPad < 0 {
				idPad = 0
			}

			// Context badge - fixed width
			// Visual badge is [XY] = 2 brackets + label
			// Calculate raw label width for pad
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

func rgbTriplet(hex string) string {
	c := strings.TrimPrefix(hex, "#")
	if len(c) != 6 {
		return "8;9;12"
	}
	r, _ := strconv.ParseUint(c[0:2], 16, 8)
	g, _ := strconv.ParseUint(c[2:4], 16, 8)
	b, _ := strconv.ParseUint(c[4:6], 16, 8)
	return fmt.Sprintf("%d;%d;%d", r, g, b)
}

func ansiBg(hex string) string {
	return "\x1b[48;2;" + rgbTriplet(hex) + "m"
}

func ansiFg(hex string) string {
	return "\x1b[38;2;" + rgbTriplet(hex) + "m"
}

const sgrAttrReset = "\x1b[22;23;24;25;27;28;29m"

// applySurfaceLine rewrites every SGR sequence in line so that any reset (0, empty, 39, 49)
// immediately restores the surface's background (bgHex) and default foreground (fgHex).
// By replacing 0/39/49 with explicit attribute/color sequences and emitting bounded SGR chunks,
// repeated calls across nested containers are 100% idempotent and never exceed ansi.MaxParams (32).
func applySurfaceLine(line string, bgHex, fgHex string) string {
	bgSeq := ansiBg(bgHex)
	fgSeq := ansiFg(fgHex)

	rewritten := sgrSeqRe.ReplaceAllStringFunc(line, func(m string) string {
		content := m[2 : len(m)-1]
		if content == "" || content == "0" || content == "00" {
			return sgrAttrReset + bgSeq + fgSeq
		}
		parts := strings.Split(content, ";")
		var b strings.Builder
		var cur []string
		flushCur := func() {
			if len(cur) > 0 {
				b.WriteString("\x1b[" + strings.Join(cur, ";") + "m")
				cur = cur[:0]
			}
		}
		for i := 0; i < len(parts); i++ {
			p := parts[i]
			if p == "38" || p == "48" || p == "58" {
				if i+1 < len(parts) && parts[i+1] == "2" && i+4 < len(parts) {
					flushCur()
					b.WriteString("\x1b[" + strings.Join(parts[i:i+5], ";") + "m")
					i += 4
					continue
				}
				if i+1 < len(parts) && parts[i+1] == "5" && i+2 < len(parts) {
					flushCur()
					b.WriteString("\x1b[" + strings.Join(parts[i:i+3], ";") + "m")
					i += 2
					continue
				}
			}
			if p == "0" || p == "00" {
				flushCur()
				b.WriteString(sgrAttrReset + bgSeq + fgSeq)
				continue
			}
			if p == "49" {
				flushCur()
				b.WriteString(bgSeq)
				continue
			}
			if p == "39" {
				flushCur()
				b.WriteString(fgSeq)
				continue
			}
			cur = append(cur, p)
			if len(cur) >= 8 {
				flushCur()
			}
		}
		flushCur()
		return b.String()
	})

	return bgSeq + fgSeq + rewritten + bgSeq + fgSeq
}

// paintBlockSurface paints each line of content onto a solid background of totalWidth.
func paintBlockSurface(content string, totalWidth int, bgHex, fgHex string) string {
	if totalWidth < 1 {
		totalWidth = 1
	}
	bgSeq := ansiBg(bgHex)
	fgSeq := ansiFg(fgHex)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		visW := lipgloss.Width(line)
		fill := totalWidth - visW
		if fill < 0 {
			fill = 0
		}
		painted := applySurfaceLine(line, bgHex, fgHex)
		lines[i] = bgSeq + fgSeq + painted + strings.Repeat(" ", fill) + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// renderSolidBox renders a bordered container where borders, vertical/horizontal padding,
// and all interior text share a continuous, non-bleeding background color.
func renderSolidBox(content string, totalWidth int, padY, padX int, bgHex, fgHex string, border lipgloss.Border, borderFgHex string) string {
	if totalWidth < 6 {
		totalWidth = 6
	}
	innerWidth := totalWidth - 2
	contentWidth := innerWidth - (padX * 2)
	if contentWidth < 1 {
		contentWidth = 1
	}

	bgSeq := ansiBg(bgHex)
	fgSeq := ansiFg(fgHex)
	bFgSeq := ansiFg(borderFgHex)

	leftPad := strings.Repeat(" ", padX)
	rightPad := strings.Repeat(" ", padX)
	emptyInner := bgSeq + fgSeq + strings.Repeat(" ", innerWidth)

	rawLines := strings.Split(content, "\n")
	var bodyRows []string

	for i := 0; i < padY; i++ {
		bodyRows = append(bodyRows, emptyInner)
	}

	for _, line := range rawLines {
		visW := lipgloss.Width(line)
		fillCount := contentWidth - visW
		if fillCount < 0 {
			fillCount = 0
		}
		painted := applySurfaceLine(line, bgHex, fgHex)
		row := bgSeq + fgSeq + leftPad + painted + strings.Repeat(" ", fillCount) + rightPad
		bodyRows = append(bodyRows, row)
	}

	for i := 0; i < padY; i++ {
		bodyRows = append(bodyRows, emptyInner)
	}

	topBorder := bgSeq + bFgSeq + border.TopLeft + strings.Repeat(border.Top, innerWidth) + border.TopRight + "\x1b[0m"
	bottomBorder := bgSeq + bFgSeq + border.BottomLeft + strings.Repeat(border.Bottom, innerWidth) + border.BottomRight + "\x1b[0m"

	var out strings.Builder
	out.WriteString(topBorder + "\n")
	for _, r := range bodyRows {
		out.WriteString(bgSeq + bFgSeq + border.Left + r + bgSeq + bFgSeq + border.Right + "\x1b[0m\n")
	}
	out.WriteString(bottomBorder)
	return out.String()
}

// renderLeftAccentBlock renders a chat message block (.tui-msg-user, .tui-msg-vraxter, .tui-msg-specialist)
// with a subtle 1-cell thin left accent bar ("│") and a uniform background across padding and wrapped content.
func renderLeftAccentBlock(content string, totalWidth int, padY, padX int, bgHex, fgHex, accentFgHex string) string {
	if totalWidth < 6 {
		totalWidth = 6
	}
	innerWidth := totalWidth - 1
	contentWidth := innerWidth - (padX * 2)
	if contentWidth < 1 {
		contentWidth = 1
	}

	bgSeq := ansiBg(bgHex)
	fgSeq := ansiFg(fgHex)
	accentSeq := ansiFg(accentFgHex)

	leftPad := strings.Repeat(" ", padX)
	rightPad := strings.Repeat(" ", padX)
	emptyRow := bgSeq + accentSeq + "│" + bgSeq + fgSeq + strings.Repeat(" ", innerWidth) + "\x1b[0m"

	var rawLines []string
	for _, l := range strings.Split(content, "\n") {
		if lipgloss.Width(l) > contentWidth {
			sub := lipgloss.NewStyle().Width(contentWidth).Render(l)
			rawLines = append(rawLines, strings.Split(sub, "\n")...)
		} else {
			rawLines = append(rawLines, l)
		}
	}

	var rows []string
	for i := 0; i < padY; i++ {
		rows = append(rows, emptyRow)
	}
	for _, line := range rawLines {
		visW := lipgloss.Width(line)
		fillCount := contentWidth - visW
		if fillCount < 0 {
			fillCount = 0
		}
		painted := applySurfaceLine(line, bgHex, fgHex)
		row := bgSeq + accentSeq + "│" + bgSeq + fgSeq + leftPad + painted + strings.Repeat(" ", fillCount) + rightPad + "\x1b[0m"
		rows = append(rows, row)
	}
	for i := 0; i < padY; i++ {
		rows = append(rows, emptyRow)
	}

	return strings.Join(rows, "\n") + "\n"
}

// renderPill renders a single-line badge/pill with a locked background that resets cleanly at its right edge.
func renderPill(text string, bgHex, fgHex string, bold bool) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fgHex))
	if bold {
		style = style.Bold(true)
	}
	rendered := style.Render(text)
	return ansiBg(bgHex) + ansiFg(fgHex) + applySurfaceLine(rendered, bgHex, fgHex) + "\x1b[0m"
}

// highlightUserToken returns a highlighted chip if the token is a /command or @specialist mention.
func highlightUserToken(tok string) string {
	if len(tok) > 1 && (strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "@")) {
		return renderPill(tok, hexAutocompleteSelBg, hexCopperSpecular, true)
	}
	return lipgloss.NewStyle().Foreground(colorIvory).Render(tok)
}

// renderUserMessage formats a SenderUser message with a leading "> " command prompt,
// highlighted /command and @specialist tokens, and a right-aligned "YOU" label inside a subtle left-accent panel.
func (m *Model) renderUserMessage(raw string, blockWidth int) string {
	if blockWidth < 20 {
		blockWidth = 20
	}
	contentWidth := blockWidth - 1 - 4 // 1 left border + 2 left pad + 2 right pad
	if contentWidth < 12 {
		contentWidth = 12
	}

	promptGlyph := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("> ")
	metaBadge := lipgloss.NewStyle().Foreground(colorAsh).Render("YOU")
	maxTextWidth := contentWidth - 2 - lipgloss.Width(metaBadge) - 2
	if maxTextWidth < 8 {
		maxTextWidth = contentWidth - 2
	}

	paragraphs := strings.Split(strings.TrimSpace(raw), "\n")
	var wrappedLines []string

	for _, para := range paragraphs {
		words := strings.Fields(para)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		var curLine strings.Builder
		curVisW := 0
		for _, w := range words {
			styledW := highlightUserToken(w)
			wVis := lipgloss.Width(styledW)
			if curVisW == 0 {
				curLine.WriteString(styledW)
				curVisW = wVis
			} else if curVisW+1+wVis <= maxTextWidth {
				curLine.WriteString(" " + styledW)
				curVisW += 1 + wVis
			} else {
				wrappedLines = append(wrappedLines, curLine.String())
				curLine.Reset()
				curLine.WriteString(styledW)
				curVisW = wVis
			}
		}
		if curLine.Len() > 0 {
			wrappedLines = append(wrappedLines, curLine.String())
		}
	}

	if len(wrappedLines) == 0 {
		wrappedLines = []string{""}
	}

	var outLines []string
	for i, l := range wrappedLines {
		if i == 0 {
			left := promptGlyph + l
			gap := contentWidth - lipgloss.Width(left) - lipgloss.Width(metaBadge)
			if gap >= 2 {
				outLines = append(outLines, left+strings.Repeat(" ", gap)+metaBadge)
			} else {
				outLines = append(outLines, left)
			}
		} else {
			outLines = append(outLines, "  "+l)
		}
	}

	return renderLeftAccentBlock(strings.Join(outLines, "\n"), blockWidth, 1, 2, hexUserBlockBg, hexIvory, hexSlate)
}

func (m *Model) renderCmdBlock(input, output string) string {
	width := m.width - 2
	if width < 40 {
		width = 40
	}
	innerWidth := width - 2

	if strings.TrimSpace(output) == "" {
		output = "Command completed."
	}

	kickerStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	cmdDisplay := strings.TrimSpace(input)
	if !strings.HasPrefix(cmdDisplay, "/") {
		cmdDisplay = "/" + cmdDisplay
	}
	cmdPill := renderPill(" "+cmdDisplay+" ", hexAutocompleteSelBg, hexCopperSpecular, true)

	linesCount := strings.Count(strings.TrimRight(output, "\n"), "\n") + 1
	metaPill := renderPill(fmt.Sprintf(" %d LINES ", linesCount), hexTitaniumDark, hexAsh, false)

	cmdText := kickerStyle.Render("◆ COMMAND OUTPUT // ") + cmdPill
	pad := innerWidth - 4 - lipgloss.Width(cmdText) - lipgloss.Width(metaPill)
	if pad < 1 {
		pad = 1
	}

	headerLine := "  " + cmdText + strings.Repeat(" ", pad) + metaPill + "  "
	header := paintBlockSurface(headerLine, innerWidth, hexCodeHeaderBg, hexIvory)

	dividerLine := lipgloss.NewStyle().Foreground(colorTitaniumMid).Render(strings.Repeat("─", innerWidth))
	divider := paintBlockSurface(dividerLine, innerWidth, hexCodeBg, hexTitaniumMid)

	var bodyLines []string
	bodyLines = append(bodyLines, "")
	for _, l := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if lipgloss.Width(l) > innerWidth-4 {
			wrapped := lipgloss.NewStyle().Width(innerWidth - 4).Render(l)
			for _, wl := range strings.Split(wrapped, "\n") {
				bodyLines = append(bodyLines, "  "+wl)
			}
		} else {
			bodyLines = append(bodyLines, "  "+l)
		}
	}
	bodyLines = append(bodyLines, "")
	body := paintBlockSurface(strings.Join(bodyLines, "\n"), innerWidth, hexCodeBg, hexStone)

	bdr := lipgloss.NormalBorder()
	bFg := ansiFg(hexTitaniumMid)
	accentFg := ansiFg(hexCopper)
	hBg := ansiBg(hexCodeHeaderBg)
	cBg := ansiBg(hexCodeBg)

	var out strings.Builder
	out.WriteString(hBg + accentFg + bdr.TopLeft + bFg + strings.Repeat(bdr.Top, innerWidth) + bdr.TopRight + "\x1b[0m\n")
	out.WriteString(hBg + accentFg + bdr.Left + strings.TrimSuffix(header, "\x1b[0m") + hBg + bFg + bdr.Right + "\x1b[0m\n")
	out.WriteString(cBg + accentFg + bdr.Left + strings.TrimSuffix(divider, "\x1b[0m") + cBg + bFg + bdr.Right + "\x1b[0m\n")
	for _, bl := range strings.Split(body, "\n") {
		out.WriteString(cBg + accentFg + bdr.Left + strings.TrimSuffix(bl, "\x1b[0m") + cBg + bFg + bdr.Right + "\x1b[0m\n")
	}
	out.WriteString(cBg + accentFg + bdr.BottomLeft + bFg + strings.Repeat(bdr.Bottom, innerWidth) + bdr.BottomRight + "\x1b[0m\n\n")
	return out.String()
}

func (m *Model) updateActiveCard(newContent string) {
	if len(m.History) == 0 {
		return
	}
	if m.ActiveCardHistoryIdx >= 0 && m.ActiveCardHistoryIdx < len(m.History) {
		m.History[m.ActiveCardHistoryIdx].Content = newContent
		return
	}
	for i := len(m.History) - 1; i >= 0; i-- {
		if m.History[i].Type == BlockComponent {
			m.History[i].Content = newContent
			return
		}
	}
	m.History[len(m.History)-1].Content = newContent
}

func (m *Model) restoreInputStyles() {
	m.Input.Prompt = "❯ "
	tas := m.Input.Styles()
	tas.Focused.Prompt = lipgloss.NewStyle().Foreground(colorCopperLight).Background(colorInputBg).Bold(true)
	tas.Focused.Base = lipgloss.NewStyle().Background(colorInputBg)
	tas.Focused.CursorLine = lipgloss.NewStyle().Background(colorInputBg)
	tas.Focused.Text = lipgloss.NewStyle().Foreground(colorIvory).Background(colorInputBg)
	m.Input.SetStyles(tas)
	m.Input.Focus()
}

// renderSubCard renders an inner bordered box with a solid background and a subtle thin colored left accent bar ("│"),
// safe to nest inside renderSolidBox without background color bleeding.
func renderSubCard(content string, totalWidth int, padY, padX int, bgHex, fgHex, leftAccentHex, borderHex string) string {
	if totalWidth < 8 {
		totalWidth = 8
	}
	innerWidth := totalWidth - 2
	contentWidth := innerWidth - (padX * 2)
	if contentWidth < 1 {
		contentWidth = 1
	}

	bgSeq := ansiBg(bgHex)
	fgSeq := ansiFg(fgHex)
	accentSeq := ansiFg(leftAccentHex)
	bFgSeq := ansiFg(borderHex)
	bdr := lipgloss.NormalBorder()

	leftPad := strings.Repeat(" ", padX)
	rightPad := strings.Repeat(" ", padX)
	emptyRow := bgSeq + accentSeq + bdr.Left + bgSeq + fgSeq + strings.Repeat(" ", innerWidth) + bgSeq + bFgSeq + bdr.Right + "\x1b[0m"

	var rawLines []string
	for _, l := range strings.Split(content, "\n") {
		if lipgloss.Width(l) > contentWidth {
			sub := lipgloss.NewStyle().Width(contentWidth).Render(l)
			rawLines = append(rawLines, strings.Split(sub, "\n")...)
		} else {
			rawLines = append(rawLines, l)
		}
	}

	var out strings.Builder
	out.WriteString(bgSeq + accentSeq + bdr.TopLeft + bFgSeq + strings.Repeat(bdr.Top, innerWidth) + bdr.TopRight + "\x1b[0m\n")
	for i := 0; i < padY; i++ {
		out.WriteString(emptyRow + "\n")
	}
	for _, line := range rawLines {
		visW := lipgloss.Width(line)
		fill := contentWidth - visW
		if fill < 0 {
			fill = 0
		}
		painted := applySurfaceLine(line, bgHex, fgHex)
		out.WriteString(bgSeq + accentSeq + bdr.Left + bgSeq + fgSeq + leftPad + painted + strings.Repeat(" ", fill) + rightPad + bgSeq + bFgSeq + bdr.Right + "\x1b[0m\n")
	}
	for i := 0; i < padY; i++ {
		out.WriteString(emptyRow + "\n")
	}
	out.WriteString(bgSeq + accentSeq + bdr.BottomLeft + bFgSeq + strings.Repeat(bdr.Bottom, innerWidth) + bdr.BottomRight + "\x1b[0m")
	return out.String()
}

// renderNotification renders a sleek, boutique telemetry notification line (BlockComponent)
// matching the aesthetic of tool/skill execution without creating an assistant message body box.
func (m *Model) renderNotification(icon, title, detail, badge string) string {
	sigilColor := colorCopperSpecular
	switch icon {
	case "✓":
		sigilColor = colorLedEmerald
	case "✕":
		sigilColor = colorLedCrimson
	case "⚙":
		sigilColor = colorSapphireBright
	case "◆":
		sigilColor = colorGold
	case "◈":
		sigilColor = colorCopperLight
	case "⚡":
		sigilColor = colorCopperSpecular
	}

	iconStyle := lipgloss.NewStyle().Foreground(sigilColor).Bold(true)
	titleStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	detailStyle := lipgloss.NewStyle().Foreground(colorIvory)
	badgeStyle := lipgloss.NewStyle().Foreground(colorAsh)

	var sb strings.Builder
	sb.WriteString("  " + iconStyle.Render(icon) + " ")
	if title != "" {
		sb.WriteString(titleStyle.Render(title) + " ")
	}
	if detail != "" {
		sb.WriteString(detailStyle.Render(detail) + " ")
	}
	if badge != "" {
		sb.WriteString(badgeStyle.Render("[" + badge + "]"))
	}
	sb.WriteString("\n\n")
	return sb.String()
}

// renderExecutionBanner renders a compact telemetry block matching .tui-execution-confirmed in the TUI CSS.
func (m *Model) renderExecutionBanner(kind, title, detail, badge string, width int) string {
	boxWidth := width - 2
	if boxWidth < 40 {
		boxWidth = 40
	}
	innerWidth := boxWidth - 6

	bgHex := "#0A1410"
	accentHex := hexLedEmerald
	borderHex := "#193829"
	icon := "✓"
	titleColor := colorLedEmerald
	badgeColor := colorCopperSpecular

	switch kind {
	case "started":
		bgHex = "#10131A"
		accentHex = hexSapphireBright
		borderHex = hexSapphireDeep
		icon = "⚙"
		titleColor = colorSapphireBright
	case "swarm":
		bgHex = hexSpecialistBlockBg
		accentHex = hexCopper
		borderHex = hexCopperLine
		icon = "⚡"
		titleColor = colorCopperLight
	case "failed":
		bgHex = "#180C0E"
		accentHex = hexLedCrimson
		borderHex = "#4A1820"
		icon = "✕"
		titleColor = colorLedCrimson
		badgeColor = colorLedCrimson
	}

	left := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(icon+" "+strings.ToUpper(title)) +
		"  " +
		lipgloss.NewStyle().Foreground(colorIvory).Render(detail)
	right := lipgloss.NewStyle().Foreground(badgeColor).Bold(true).Render(badge)

	gap := innerWidth - lipgloss.Width(left) - lipgloss.Width(right)
	var row string
	if gap >= 2 {
		row = left + strings.Repeat(" ", gap) + right
	} else {
		row = left + "\n" + right
	}

	return renderSubCard(row, boxWidth, 0, 2, bgHex, hexIvory, accentHex, borderHex) + "\n"
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

	boxWidth := width - 2
	if boxWidth < 54 {
		boxWidth = 54
	}
	contentWidth := boxWidth - 6 // 2 border + 4 horizontal padding

	accentStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
	kickerStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colorAsh)
	stoneStyle := lipgloss.NewStyle().Foreground(colorStone)
	ivoryBold := lipgloss.NewStyle().Foreground(colorIvory).Bold(true)
	divider := lipgloss.NewStyle().Foreground(colorCopperLine).Render(strings.Repeat("─", contentWidth))

	estText := strings.TrimSpace(plan.Estimations)
	if estText == "" {
		estText = "DETERMINISTIC"
	}

	outerBorderHex := hexCopperSpecular
	var statusBadge string
	switch {
	case strings.Contains(status, "ACCEPTED"):
		outerBorderHex = hexLedEmerald
		statusBadge = renderPill(" ✓ "+status+" ", hexLedEmerald, hexVoid, true)
	case strings.Contains(status, "AMENDED"):
		outerBorderHex = hexCopper
		statusBadge = renderPill(" ↻ "+status+" ", "#261B0E", hexCopperLight, true)
	case status != "":
		outerBorderHex = hexLedCrimson
		statusBadge = renderPill(" ✕ "+status+" ", hexLedCrimson, hexVoid, true)
	default:
		outerBorderHex = hexCopperSpecular
		statusBadge = renderPill(" ● AWAITING HITL DECISION ", "#261B0E", hexLedAmber, true)
	}

	var doc strings.Builder

	// 1. Header Bar (.vraxter-block-head / .arc-header)
	headerLeft := accentStyle.Render(`VRAXTER SUPERVISOR // .VRX IMPLEMENTATION STRATEGY`)
	hPad := contentWidth - lipgloss.Width(headerLeft) - lipgloss.Width(statusBadge)
	if hPad < 1 {
		hPad = 1
	}
	doc.WriteString(headerLeft + strings.Repeat(" ", hPad) + statusBadge + "\n")
	doc.WriteString(divider + "\n")

	// 2. Executive Objective & Architectural Rationale Sub-Card
	subInnerWidth := contentWidth - 6
	if subInnerWidth < 20 {
		subInnerWidth = 20
	}

	var summaryBuf strings.Builder
	objKicker := kickerStyle.Render("◈ MISSION OBJECTIVE")
	phaseCountPill := renderPill(fmt.Sprintf(" %d PHASES · %s ", len(plan.Phases), strings.ToUpper(estText)), hexSapphireDeep, hexSapphireBright, true)
	objGap := subInnerWidth - lipgloss.Width(objKicker) - lipgloss.Width(phaseCountPill)
	if objGap < 1 {
		objGap = 1
	}
	summaryBuf.WriteString(objKicker + strings.Repeat(" ", objGap) + phaseCountPill + "\n")

	goalWrapped := lipgloss.NewStyle().Foreground(colorIvory).Bold(true).Width(subInnerWidth).Render(strings.TrimSpace(plan.Goal))
	summaryBuf.WriteString(goalWrapped)

	if strings.TrimSpace(plan.Reasoning) != "" {
		subDiv := lipgloss.NewStyle().Foreground(colorTitaniumMid).Render(strings.Repeat("─", subInnerWidth))
		summaryBuf.WriteString("\n" + subDiv + "\n")
		summaryBuf.WriteString(lipgloss.NewStyle().Foreground(colorCopper).Bold(true).Render("◈ ARCHITECTURAL RATIONALE") + "\n")
		reasonWrapped := lipgloss.NewStyle().Foreground(colorStone).Width(subInnerWidth).Render(strings.TrimSpace(plan.Reasoning))
		summaryBuf.WriteString(reasonWrapped)
	}

	if strings.TrimSpace(plan.Narrative) != "" {
		empty := ""
		zero := uint(0)
		gold := "#E2A576"
		emerald := "#3CE68A"
		orange := "#C87D4B"
		ivory := "#F4F1EA"

		ns := styles.DarkStyleConfig
		ns.Document.BackgroundColor = &empty
		ns.Document.Color = &ivory
		ns.Document.Margin = &zero
		ns.Paragraph.BackgroundColor = &empty
		ns.Paragraph.Margin = &zero
		ns.Text.BackgroundColor = &empty
		ns.Text.Color = &ivory
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
			glamour.WithWordWrap(subInnerWidth),
		)
		rendered, _ := r.Render(plan.Narrative)
		rendered = strings.TrimSpace(utils.StripANSIBackgrounds(rendered))
		if rendered != "" {
			subDiv := lipgloss.NewStyle().Foreground(colorTitaniumMid).Render(strings.Repeat("─", subInnerWidth))
			summaryBuf.WriteString("\n" + subDiv + "\n")
			summaryBuf.WriteString(mutedStyle.Bold(true).Render("◈ STRATEGY NOTES") + "\n")
			summaryBuf.WriteString(rendered)
		}
	}

	doc.WriteString(renderSubCard(summaryBuf.String(), contentWidth, 0, 2, hexCodeHeaderBg, hexIvory, hexCopper, hexTitaniumMid) + "\n\n")

	// 3. Execution Phases Stack (.plan-step-pill sub-cards)
	doc.WriteString(kickerStyle.Render("// EXECUTION PHASES & SPECIALIST ROUTING") + "\n")

	for i, p := range plan.Phases {
		var phaseBuf strings.Builder

		phaseNumPill := renderPill(fmt.Sprintf(" PHASE %02d ", i+1), "#271E18", hexCopperSpecular, true)
		specName := strings.TrimPrefix(strings.TrimSpace(p.Specialist), "@")
		if specName == "" {
			specName = "supervisor"
		}
		specPill := renderPill(" ⚡ @"+strings.ToUpper(specName)+" ", "#0E2018", hexLedEmerald, true)

		titleText := ivoryBold.Render(strings.TrimSpace(p.Title))
		leftHead := phaseNumPill + "  " + titleText
		pGap := subInnerWidth - lipgloss.Width(leftHead) - lipgloss.Width(specPill)
		if pGap >= 1 {
			phaseBuf.WriteString(leftHead + strings.Repeat(" ", pGap) + specPill)
		} else {
			phaseBuf.WriteString(leftHead + "\n" + specPill)
		}

		if strings.TrimSpace(p.Description) != "" || len(p.Tasks) > 0 {
			subDiv := lipgloss.NewStyle().Foreground(colorTitaniumMid).Render(strings.Repeat("─", subInnerWidth))
			phaseBuf.WriteString("\n" + subDiv)
		}

		if strings.TrimSpace(p.Description) != "" {
			descWrapped := stoneStyle.Width(subInnerWidth).Render(strings.TrimSpace(p.Description))
			phaseBuf.WriteString("\n" + descWrapped)
		}

		if len(p.Tasks) > 0 {
			bullet := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("▸ ")
			taskWrapW := subInnerWidth - 3
			if taskWrapW < 10 {
				taskWrapW = 10
			}
			for _, t := range p.Tasks {
				wrappedTask := lipgloss.NewStyle().Foreground(colorIvory).Width(taskWrapW).Render(strings.TrimSpace(t))
				tLines := strings.Split(wrappedTask, "\n")
				for li, tl := range tLines {
					if li == 0 {
						phaseBuf.WriteString("\n " + bullet + tl)
					} else {
						phaseBuf.WriteString("\n   " + tl)
					}
				}
			}
		}

		doc.WriteString(renderSubCard(phaseBuf.String(), contentWidth, 0, 2, hexCodeBg, hexIvory, hexSapphireBright, hexTitaniumMid) + "\n")
	}

	// 4. Footer Telemetry Bar
	doc.WriteString(divider + "\n")
	estLeft := kickerStyle.Render("⏳ ESTIMATE: ") +
		ivoryBold.Render(estText) +
		mutedStyle.Render("  ·  SEMANTIC HANDOFF MASK ACTIVE")

	var footerRight string
	switch {
	case strings.Contains(status, "ACCEPTED"):
		footerRight = lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true).Render("SWARM DISPATCHED · DAEMON RUNNING")
	case strings.Contains(status, "AMENDED"):
		footerRight = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("RE-SYNTHESIZING STRATEGY")
	case status != "":
		footerRight = lipgloss.NewStyle().Foreground(colorLedCrimson).Bold(true).Render("EXECUTION ABORTED")
	default:
		footerRight = lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("←/→ SELECT · ↵ CONFIRM · ESC REJECT")
	}

	fGap := contentWidth - lipgloss.Width(estLeft) - lipgloss.Width(footerRight)
	if fGap >= 1 {
		doc.WriteString(estLeft + strings.Repeat(" ", fGap) + footerRight)
	} else {
		doc.WriteString(estLeft + "\n" + footerRight)
	}

	return renderSolidBox(doc.String(), boxWidth, 1, 2, hexApprovalCardBg, hexIvory, lipgloss.NormalBorder(), outerBorderHex) + "\n"
}

// renderSkillApproval visually formats the Skill Approval prompt (.tui-approval-request-card in CSS)
func (m *Model) renderSkillApproval(skillID, payload string, width int, status string) string {
	boxWidth := width - 2
	if boxWidth < 54 {
		boxWidth = 54
	}
	contentWidth := boxWidth - 6

	var doc strings.Builder

	headerTitle := lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("🔒 ENTERPRISE POLICY: MANUAL SKILL APPROVAL REQUIRED")
	outerBorderHex := hexCopperSpecular
	var statusBadge string
	if status != "" {
		if strings.Contains(status, "APPROVED") {
			outerBorderHex = hexLedEmerald
			statusBadge = renderPill(" ✓ "+status+" ", hexLedEmerald, hexVoid, true)
		} else {
			outerBorderHex = hexLedCrimson
			statusBadge = renderPill(" ✕ "+status+" ", hexLedCrimson, hexVoid, true)
		}
	} else {
		statusBadge = renderPill(" ● WAITING FOR INPUT DECISION ", "#261B0E", hexLedAmber, true)
	}

	hPad := contentWidth - lipgloss.Width(headerTitle) - lipgloss.Width(statusBadge)
	if hPad < 1 {
		hPad = 1
	}
	doc.WriteString(headerTitle + strings.Repeat(" ", hPad) + statusBadge + "\n")
	doc.WriteString(lipgloss.NewStyle().Foreground(colorCopperLine).Render(strings.Repeat("─", contentWidth)) + "\n")

	kStyle := lipgloss.NewStyle().Foreground(colorAsh)
	vStyle := lipgloss.NewStyle().Foreground(colorIvory).Bold(true)
	cStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular)

	var isoBadge string
	switch m.IsolationMode {
	case "local_lan":
		isoBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7")).Bold(true).Render("LOCAL_LAN")
	case "hybrid":
		isoBadge = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("HYBRID")
	default:
		isoBadge = lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true).Render("🛡️ AIRGAP (ZERO EGRESS)")
	}

	metaRow := kStyle.Render("SKILL ID & BINARY: ") + vStyle.Render(skillID) + " " + cStyle.Render("(Wazero WASI Sandbox)") +
		"   " + kStyle.Render("PERIMETER: ") + isoBadge
	doc.WriteString(metaRow + "\n\n")

	var paramContent string
	var call types.ToolCall
	if err := json.Unmarshal([]byte(payload), &call); err == nil {
		pretty, _ := json.MarshalIndent(call.Params, "", "  ")
		paramContent = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("◆ INVOCATION PAYLOAD") + "\n" +
			lipgloss.NewStyle().Foreground(colorStone).Render(string(pretty))
	} else {
		paramContent = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("◆ INVOCATION PAYLOAD") + "\n" +
			lipgloss.NewStyle().Foreground(colorStone).Render(payload)
	}
	doc.WriteString(renderSubCard(paramContent, contentWidth, 0, 2, hexCodeBg, hexStone, hexSapphireBright, hexTitaniumMid))

	return renderSolidBox(doc.String(), boxWidth, 1, 2, hexApprovalCardBg, hexIvory, lipgloss.NormalBorder(), outerBorderHex) + "\n"
}

// renderDecisionBridge renders the interactive approval bar directly inside the Input Shelf (.tui-input-shelf.is-approval)
func (m *Model) renderDecisionBridge(width int) string {
	if m.PendingRunSkill != "" {
		return m.renderSkillRunPrompt(width)
	}
	contentWidth := width - 6
	if contentWidth < 20 {
		contentWidth = 20
	}

	var proceedBtn, rejectBtn, amendBtn string
	if m.PlanActionIndex == 0 {
		proceedBtn = renderPill(" ✓ ACCEPT / PROCEED [↵] ", hexLedEmerald, hexVoid, true)
	} else {
		proceedBtn = renderPill(" ✓ ACCEPT / PROCEED [↵] ", "#0E2018", hexLedEmerald, true)
	}

	if m.PlanActionIndex == 1 {
		rejectBtn = renderPill(" ✕ DENY / REJECT [ESC] ", hexLedCrimson, hexVoid, true)
	} else {
		rejectBtn = renderPill(" ✕ DENY / REJECT [ESC] ", "#221114", hexLedCrimson, true)
	}

	buttons := proceedBtn + "  " + rejectBtn
	labelText := "◈ AUTHORIZE PLAN EXECUTION"
	if m.IsWaitingForSkillApproval {
		labelText = "◈ AUTHORIZE SKILL EXECUTION"
	} else {
		if m.PlanActionIndex == 2 {
			amendBtn = renderPill(" AMEND ", hexLedAmber, hexVoid, true)
		} else {
			amendBtn = renderPill(" AMEND ", "#211810", hexCopperLight, true)
		}
		buttons = buttons + "  " + amendBtn
	}

	amberDot := lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("━ ")
	label := amberDot + lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render(labelText)

	gap := contentWidth - lipgloss.Width(label) - lipgloss.Width(buttons)
	if gap < 1 {
		gap = 1
	}
	row := label + strings.Repeat(" ", gap) + buttons

	content := row
	if m.ShowAmendInput {
		m.Input.Prompt = "❯ FEEDBACK: "
		tas := m.Input.Styles()
		tas.Focused.Prompt = lipgloss.NewStyle().Foreground(colorCopper).Background(colorApprovalShelfBg).Bold(true)
		tas.Focused.Base = lipgloss.NewStyle().Background(colorApprovalShelfBg)
		tas.Focused.CursorLine = lipgloss.NewStyle().Background(colorApprovalShelfBg)
		tas.Focused.Text = lipgloss.NewStyle().Foreground(colorIvory).Background(colorApprovalShelfBg)
		m.Input.SetStyles(tas)
		div := lipgloss.NewStyle().Foreground(colorCopperLine).Render(strings.Repeat("─", contentWidth))
		content = row + "\n" + div + "\n" + m.Input.View()
	}

	return renderSolidBox(content, width, 0, 2, hexApprovalShelfBg, hexIvory, lipgloss.NormalBorder(), hexCopperSpecular)
}

func (m *Model) renderSkillRunPrompt(width int) string {
	contentWidth := width - 6
	if contentWidth < 20 {
		contentWidth = 20
	}

	var runBtn, dismissBtn string
	if m.PlanActionIndex == 0 {
		runBtn = renderPill(" ✓ RUN NOW [↵] ", hexLedEmerald, hexVoid, true)
	} else {
		runBtn = renderPill(" ✓ RUN NOW [↵] ", "#0E2018", hexLedEmerald, true)
	}

	if m.PlanActionIndex == 1 {
		dismissBtn = renderPill(" DONE [ESC] ", hexAsh, hexVoid, true)
	} else {
		dismissBtn = renderPill(" DONE [ESC] ", hexTitaniumMid, hexStone, true)
	}

	buttons := runBtn + "  " + dismissBtn
	amberDot := lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("━ ")
	label := amberDot + lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render(fmt.Sprintf("◈ SKILL READY: %s", m.PendingRunSkill))

	gap := contentWidth - lipgloss.Width(label) - lipgloss.Width(buttons)
	if gap < 1 {
		gap = 1
	}
	row := label + strings.Repeat(" ", gap) + buttons

	return renderSolidBox(row, width, 0, 2, hexApprovalShelfBg, hexIvory, lipgloss.NormalBorder(), hexCopperSpecular)
}

// renderCommandDeck renders the Top Rail (.deck-top-rail) of the Double-Rail Command Deck.
func (m *Model) renderCommandDeck() string {
	panelWidth := m.width - 2
	if panelWidth < 20 {
		panelWidth = 20
	}

	outerLineStyle := lipgloss.NewStyle().Foreground(colorSlate)
	innerLineStyle := lipgloss.NewStyle().Foreground(colorCopperLine)
	shortcutStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)

	shortcutText := " /? HELP • Shift+↵ NEWLINE • ^C QUIT "
	if m.IsWaitingForApproval || m.IsWaitingForSkillApproval {
		shortcutText = " ←/→ NAVIGATE • ENTER CONFIRM • ESC REJECT "
	}
	shortcuts := shortcutStyle.Render(shortcutText)

	// Running jobs pill (.rail-jobs-pill)
	activeJobsPill := ""
	if len(m.ActiveJobs) > 0 {
		var titles []string
		for _, j := range m.ActiveJobs {
			titles = append(titles, j.Title)
		}
		label := fmt.Sprintf(" ⚙ %d RUNNING: %s ", len(m.ActiveJobs), strings.Join(titles, ", "))
		if len(label) > 38 {
			label = label[:35] + "... "
		}
		activeJobsPill = " " + renderPill(label, hexCopper, hexVoid, true) + " "
	}

	// Focus Identity Pill (.rail-focus-pill)
	identityLabel := "FOCUS: SUPERVISOR FOCUS"
	if m.IsWaitingForApproval || m.IsWaitingForSkillApproval {
		identityLabel = "FOCUS: VRAXTER SHIELD (HITL GATE)"
	} else if m.SpecialistBanner != "" {
		identityLabel = fmt.Sprintf("FOCUS: @%s", m.SpecialistBanner)
	}
	focusBorder := lipgloss.NewStyle().Foreground(colorCopperLineStrong).Render("│")
	focusInner := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render(" " + identityLabel + " ")
	focusPill := ansiBg(hexFocusPillBg) + applySurfaceLine(focusBorder+focusInner+focusBorder, hexFocusPillBg, hexCopperLight) + "\x1b[0m"

	// Ambient Isolation Capsule (.rail-isolation-pill)
	isolationCapsule := ""
	switch m.IsolationMode {
	case "local_lan":
		isoBorder := lipgloss.NewStyle().Foreground(lipgloss.Color("#2E3A4E")).Render("│")
		isoInner := lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7")).Bold(true).Render(" 🌐 LAN ONLY ")
		isolationCapsule = " " + ansiBg("#12161F") + isoBorder + isoInner + isoBorder + "\x1b[0m"
	case "hybrid":
		isoBorder := lipgloss.NewStyle().Foreground(colorCopperLineStrong).Render("│")
		isoInner := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render(" ⚡ HYBRID ")
		isolationCapsule = " " + ansiBg("#20150E") + isoBorder + isoInner + isoBorder + "\x1b[0m"
	default: // "airgap"
		lockLabel := "AIRGAP LOCKED"
		if !m.IsAirgapLocked && m.IsolationMode != "airgap" && m.IsolationMode != "" {
			lockLabel = "AIRGAP STRICT"
		}
		isoBorder := lipgloss.NewStyle().Foreground(lipgloss.Color("#1B4D3E")).Render("│")
		isoInner := lipgloss.NewStyle().Foreground(lipgloss.Color("#38FFA3")).Bold(true).Render(" 🛡️ " + lockLabel + " ")
		isolationCapsule = " " + ansiBg("#0A1A12") + isoBorder + isoInner + isoBorder + "\x1b[0m"
	}

	leftPart := outerLineStyle.Render("──") + shortcuts + activeJobsPill
	rightPart := isolationCapsule + " " + focusPill + outerLineStyle.Render(" ──")

	gapWidth := panelWidth - lipgloss.Width(leftPart) - lipgloss.Width(rightPart)
	if gapWidth < 1 {
		gapWidth = 1
	}
	filler := innerLineStyle.Render(strings.Repeat("─", gapWidth))

	return paintBlockSurface(" "+leftPart+filler+rightPart+" ", m.width, hexTuiBg, hexAsh)
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

func (m Model) listenForSystemEvents(ch <-chan *v1.SystemEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil // Stream closed
		}
		return SystemEventMsgWrapper{Event: event}
	}
}

func (m *Model) View() tea.View {
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
		if spw, ok := m.ActiveSetup.(*SpecialistWizard); ok {
			spw.Width = m.width
		}
		content := "\033]0;Vraxter Workstation\007" + m.ActiveSetup.View().Content
		v := tea.NewView(content)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	if !m.Ready {
		v := tea.NewView("\033]0;Vraxter Workstation\007Initializing Dashboard...")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	panelWidth := m.width - 2
	if panelWidth < 20 {
		panelWidth = 20
	}

	// 1. Welcome Header (.tui-welcome-header)
	header := " " + strings.ReplaceAll(m.renderHeader(), "\n", "\n ")

	// 2. Top Rail of Command Deck (.deck-top-rail)
	commandDeck := m.renderCommandDeck()

	// 3. Autocomplete Overlay (.tui-autocomplete-overlay)
	var autocompleteBlock string
	if m.Autocomplete.IsActive && len(m.Autocomplete.Items) > 0 {
		autocompleteBlock = " " + strings.ReplaceAll(m.renderAutocomplete(), "\n", "\n ")
	}

	// 4. Input Shelf (.tui-input-shelf: Active vs Inactive Streaming vs Approval Decision Bridge)
	var rawInputShelf string
	if m.IsWaitingForApproval || m.IsWaitingForSkillApproval {
		rawInputShelf = m.renderDecisionBridge(panelWidth)
	} else if m.IsStreaming || m.IsPlanning {
		contentWidth := panelWidth - 6
		if contentWidth < 10 {
			contentWidth = 10
		}
		chevron := lipgloss.NewStyle().Foreground(colorSlate).Bold(true).Render("❯ ")
		procLabel := "Vraxter is processing..."
		badgeLabel := " INPUT LOCKED · STREAMING "
		if m.IsPlanning {
			procLabel = "Synthesizing .vrx implementation strategy..."
			badgeLabel = " INPUT LOCKED · PLANNING "
		}
		procText := lipgloss.NewStyle().Foreground(colorAsh).Render(procLabel)
		left := chevron + procText
		badge := renderPill(badgeLabel, hexInactiveBadgeBg, hexAsh, false)
		gap := contentWidth - lipgloss.Width(left) - lipgloss.Width(badge)
		if gap < 1 {
			gap = 1
		}
		row := left + strings.Repeat(" ", gap) + badge
		rawInputShelf = renderSolidBox(row, panelWidth, 0, 2, hexTuiBg, hexAsh, lipgloss.NormalBorder(), hexTitaniumMid)
	} else {
		m.Input.SetWidth(panelWidth - 6)
		rawInputShelf = renderSolidBox(m.Input.View(), panelWidth, 0, 2, hexInputBg, hexIvory, lipgloss.NormalBorder(), hexCopperLineStrong)
	}
	inputShelf := " " + strings.ReplaceAll(rawInputShelf, "\n", "\n ")

	// 5. Bottom Rail (.deck-bottom-rail)
	watermark := fmt.Sprintf(" © %d Vraxter · Sovereign Autonomous AI Engine ", time.Now().Year())
	wmStyle := lipgloss.NewStyle().Foreground(colorAsh).Render(watermark)
	wmWidth := lipgloss.Width(wmStyle)
	lineStyle := lipgloss.NewStyle().Foreground(colorTitaniumLight)

	var bottomRailInner string
	if panelWidth > wmWidth+4 {
		leftWidth := (panelWidth - wmWidth) / 2
		rightWidth := panelWidth - leftWidth - wmWidth
		bottomRailInner = lineStyle.Render(strings.Repeat("─", leftWidth)) + wmStyle + lineStyle.Render(strings.Repeat("─", rightWidth))
	} else {
		bottomRailInner = lineStyle.Render(strings.Repeat("─", panelWidth))
	}
	bottomRail := paintBlockSurface(" "+bottomRailInner+" ", m.width, hexTuiBg, hexAsh)

	// 6. Sticky Status Bar (System Feedback)
	var statusBar string
	if m.StatusMessage != "" {
		statusBar = paintBlockSurface(" 💡 "+m.StatusMessage, m.width, hexTitaniumDark, hexCopperLight)
	}

	// Calculate dynamic Viewport height so layout always fits screen
	usedHeight := lipgloss.Height(header) + lipgloss.Height(commandDeck) + lipgloss.Height(inputShelf) + lipgloss.Height(bottomRail)
	if autocompleteBlock != "" {
		usedHeight += lipgloss.Height(autocompleteBlock)
	}
	if statusBar != "" {
		usedHeight += lipgloss.Height(statusBar)
	}
	chatHeight := m.height - usedHeight
	if chatHeight < 3 {
		chatHeight = 3
	}
	m.Viewport.SetWidth(panelWidth)
	m.Viewport.SetHeight(chatHeight)

	chatArea := lipgloss.NewStyle().
		Padding(0, 1).
		Height(chatHeight).
		Render(m.Viewport.View())

	var sections []string
	sections = append(sections, header, chatArea)
	if autocompleteBlock != "" {
		sections = append(sections, autocompleteBlock)
	}
	sections = append(sections, commandDeck, inputShelf, bottomRail)
	if statusBar != "" {
		sections = append(sections, statusBar)
	}

	fullScreen := paintBlockSurface(lipgloss.JoinVertical(lipgloss.Left, sections...), m.width, hexTuiBg, hexIvory)
	content := "\033]0;Vraxter Workstation\007" + fullScreen
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) renderAutocomplete() string {
	if !m.Autocomplete.IsActive || len(m.Autocomplete.Items) == 0 {
		return ""
	}

	panelWidth := m.width - 2
	if panelWidth < 40 {
		panelWidth = 40
	}
	contentWidth := panelWidth - 6

	headTitle := "// WORKSTATION COMMANDS (/ AUTOCOMPLETE OVERLAY)"
	if m.Autocomplete.Type == "specialist" {
		headTitle = "// SPECIALIST SWARM ROSTER (@ AUTOCOMPLETE OVERLAY)"
	}
	headLeft := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render(headTitle)
	headRight := lipgloss.NewStyle().Foreground(colorCopperLight).Render("↑/↓ SELECT · TAB/ENTER COMPLETE")
	headGap := contentWidth - lipgloss.Width(headLeft) - lipgloss.Width(headRight)
	if headGap < 1 {
		headGap = 1
	}

	var sb strings.Builder
	sb.WriteString(headLeft + strings.Repeat(" ", headGap) + headRight + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(colorCopperLine).Render(strings.Repeat("─", contentWidth)) + "\n")

	maxItems := 5
	startIdx := 0
	if m.Autocomplete.Cursor >= maxItems {
		startIdx = m.Autocomplete.Cursor - maxItems + 1
	}

	for i := startIdx; i < len(m.Autocomplete.Items) && i < startIdx+maxItems; i++ {
		item := m.Autocomplete.Items[i]
		if i == m.Autocomplete.Cursor {
			left := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render(" ❯ " + item)
			right := lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true).Render("[SELECTED ↵] ")
			gap := contentWidth - lipgloss.Width(left) - lipgloss.Width(right)
			if gap < 1 {
				gap = 1
			}
			row := left + strings.Repeat(" ", gap) + right
			sb.WriteString(paintBlockSurface(row, contentWidth, hexAutocompleteSelBg, hexIvory) + "\n")
		} else {
			left := lipgloss.NewStyle().Foreground(colorAsh).Render("   " + item)
			right := lipgloss.NewStyle().Foreground(colorSlate).Render("STANDBY ")
			gap := contentWidth - lipgloss.Width(left) - lipgloss.Width(right)
			if gap < 1 {
				gap = 1
			}
			row := left + strings.Repeat(" ", gap) + right
			sb.WriteString(paintBlockSurface(row, contentWidth, hexAutocompleteBg, hexAsh) + "\n")
		}
	}

	return renderSolidBox(strings.TrimRight(sb.String(), "\n"), panelWidth, 0, 2, hexAutocompleteBg, hexAsh, lipgloss.NormalBorder(), hexCopper)
}

func (m *Model) renderMarkdown(history []HistoryBlock) string {
	var fullContent strings.Builder

	blockWidth := m.width - 2
	if blockWidth < 20 {
		blockWidth = 20
	}

	for _, block := range history {
		var content string
		switch block.Type {
		case BlockMarkdown:
			content = block.Content
			content = stripANSI(content)
			content = sanitizePromptLeaks(content)

			if block.Sender == SenderUser {
				if strings.TrimSpace(content) != "" {
					fullContent.WriteString(m.renderUserMessage(content, blockWidth) + "\n")
				}
				continue
			}

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

				messageText.WriteString(m.renderCodeBlock(lang, code, blockWidth-6))

				lastIndex = match[1]
			}

			// 3. Render remaining text
			if lastIndex < len(content) {
				textSegment := content[lastIndex:]
				out := m.renderMarkdownString(textSegment)
				messageText.WriteString(out)
			}

			// --- BLOCK WRAPPER ---
			finalBlockContent := strings.TrimSpace(messageText.String())
			if finalBlockContent == "" {
				continue
			}

			switch block.Sender {
			case SenderVraxter:
				fullContent.WriteString(renderLeftAccentBlock(finalBlockContent, blockWidth, 1, 2, hexVraxterBlockBg, hexIvory, hexCopperSpecular) + "\n")
			case SenderSpecialist:
				fullContent.WriteString(renderLeftAccentBlock(finalBlockContent, blockWidth, 1, 2, hexSpecialistBlockBg, hexStone, hexCopperLight) + "\n")
			default:
				fullContent.WriteString(finalBlockContent + "\n")
			}

		case BlockThought:
			thoughtText := strings.TrimSpace(block.Content)
			if thoughtText != "" {
				header := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("◈ NEURAL CHAIN OF THOUGHT") + "\n"
				body := thoughtStyle.Render(thoughtText)
				fullContent.WriteString(renderLeftAccentBlock(header+body, blockWidth, 0, 2, hexCodeHeaderBg, hexAsh, hexSlate) + "\n")
			}

		case BlockComponent:
			// Component block: render directly bypassing Glamour
			fullContent.WriteString(block.Content)
		}
	}

	// 3. Static 2x4 Sigil Neural Link Thinking / Planning Indicator
	if (m.IsStreaming && !m.HasSentVraxterBanner) || m.IsPlanning {
		sigilLines := strings.Split(m.Spinner.View(), "\n")
		row0 := `\/\/`
		row1 := `/\/\`
		if len(sigilLines) >= 2 {
			row0 = sigilLines[0]
			row1 = sigilLines[1]
		} else if len(sigilLines) == 1 {
			row0 = sigilLines[0]
		}

		titleLabel := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("◈ VRAXTER SUPERVISOR")
		subKicker := lipgloss.NewStyle().Foreground(colorAsh).Render(" // NEURAL ENGINE ACTIVE")
		statusLabel := "Synthesizing response & routing context..."
		if m.IsPlanning {
			subKicker = lipgloss.NewStyle().Foreground(colorCopperLight).Render(" // ARCHITECTURE PLANNER DAEMON")
			statusLabel = "Decomposing objective into multi-phase .vrx execution strategy..."
		} else if m.StatusMessage != "" {
			statusLabel = m.StatusMessage
		}
		amberSlit := lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("━ ")
		statusLine := amberSlit + lipgloss.NewStyle().Foreground(colorStone).Render(statusLabel)

		thinkingContent := row0 + "   " + titleLabel + subKicker + "\n" +
			row1 + "   " + statusLine

		fullContent.WriteString(renderLeftAccentBlock(thinkingContent, blockWidth, 1, 2, hexVraxterBlockBg, hexIvory, hexCopperSpecular) + "\n")
	}

	return fullContent.String()
}

func (m *Model) renderMarkdownString(content string) string {
	out, err := m.renderer.Render(content)
	if err != nil {
		return content
	}

	out = utils.StripANSIBackgrounds(out)
	return out
}

var promptLeakRe = regexp.MustCompile(`(?s)\[(?:PROMPT LOCK|PROTOCOL LOCK|CRITICAL|TOOL USAGE|CAPABILITY LOCK|RETRY LOCK).*?\]`)

func sanitizePromptLeaks(content string) string {
	return promptLeakRe.ReplaceAllString(content, "")
}

func (m *Model) renderCodeBlock(lang, code string, width int) string {
	if lang == "" {
		lang = "CODE"
	}
	if width < 20 {
		width = 20
	}
	innerWidth := width - 2

	reANSI := regexp.MustCompile("\x1b\\[[0-9;]*m")
	lang = reANSI.ReplaceAllString(lang, "")
	lang = strings.ToUpper(lang)

	var inner string
	if lang == "CODE" {
		inner = strings.TrimSpace(code)
	} else {
		var buf bytes.Buffer
		err := quick.Highlight(&buf, strings.TrimSpace(code), strings.ToLower(lang), "terminal16m", "dracula")
		if err == nil {
			inner = utils.StripANSIBackgrounds(buf.String())
		} else {
			inner = strings.TrimSpace(code)
		}
	}

	dotStyle := lipgloss.NewStyle().Foreground(colorCopperLight)
	langStyle := dotStyle.Bold(true)
	headerLine := " " + dotStyle.Render("◆ ") + langStyle.Render(lang)
	header := paintBlockSurface(headerLine, innerWidth, hexCodeHeaderBg, hexCopperLight)

	dividerLine := lipgloss.NewStyle().Foreground(colorTitaniumMid).Render(strings.Repeat("─", innerWidth))
	divider := paintBlockSurface(dividerLine, innerWidth, hexCodeBg, hexTitaniumMid)

	var bodyLines []string
	for _, line := range strings.Split(inner, "\n") {
		bodyLines = append(bodyLines, "  "+line)
	}
	body := paintBlockSurface(strings.Join(bodyLines, "\n"), innerWidth, hexCodeBg, hexStone)

	bdr := lipgloss.RoundedBorder()
	bFg := ansiFg(hexTitaniumMid)
	sFg := ansiFg(hexSapphireBright)
	hBg := ansiBg(hexCodeHeaderBg)
	cBg := ansiBg(hexCodeBg)

	var out strings.Builder
	out.WriteString(hBg + sFg + bdr.TopLeft + bFg + strings.Repeat(bdr.Top, innerWidth) + bdr.TopRight + "\x1b[0m\n")
	out.WriteString(hBg + sFg + bdr.Left + strings.TrimSuffix(header, "\x1b[0m") + hBg + bFg + bdr.Right + "\x1b[0m\n")
	out.WriteString(cBg + sFg + bdr.Left + strings.TrimSuffix(divider, "\x1b[0m") + cBg + bFg + bdr.Right + "\x1b[0m\n")
	for _, bl := range strings.Split(body, "\n") {
		out.WriteString(cBg + sFg + bdr.Left + strings.TrimSuffix(bl, "\x1b[0m") + cBg + bFg + bdr.Right + "\x1b[0m\n")
	}
	out.WriteString(cBg + sFg + bdr.BottomLeft + bFg + strings.Repeat(bdr.Bottom, innerWidth) + bdr.BottomRight + "\x1b[0m\n")
	return "\n" + out.String()
}

func stripANSI(str string) string {
	const ansi = "[\u001B\u009B][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]"
	var re = regexp.MustCompile(ansi)
	return re.ReplaceAllString(str, "")
}

func buildRenderer(width int) (*glamour.TermRenderer, error) {
	empty := ""
	zero := uint(0)
	ivory := "#F4F1EA"
	stone := "#B8B5AE"
	copperSpecular := "#F7D4B2"
	copperLight := "#E2A576"
	copper := "#C87D4B"
	copperMid := "#A65E30"
	sapphireBright := "#5B8FD9"
	titaniumLight := "#2A303C"

	s := styles.DarkStyleConfig

	// Document
	s.Document.BackgroundColor = &empty
	s.Document.Color = &ivory
	s.Document.Margin = &zero

	// Paragraph - The main culprit for background-per-line artifacts
	s.Paragraph.BackgroundColor = &empty
	s.Paragraph.Margin = &zero

	// Text base
	s.Text.BackgroundColor = &empty
	s.Text.Color = &ivory
	s.Strong.Color = &copperSpecular
	s.Emph.Color = &copperLight

	// Headings
	s.Heading.BackgroundColor = &empty
	s.Heading.Margin = &zero
	s.H1.BackgroundColor = &empty
	s.H1.Color = &copperSpecular
	s.H2.BackgroundColor = &empty
	s.H2.Color = &copperLight
	s.H3.BackgroundColor = &empty
	s.H3.Color = &copper
	s.H4.BackgroundColor = &empty
	s.H4.Color = &copperMid

	// Lists
	s.List.BackgroundColor = &empty
	s.Item.BackgroundColor = &empty

	// Blockquote
	s.BlockQuote.BackgroundColor = &empty
	s.BlockQuote.Color = &stone
	s.BlockQuote.Margin = &zero
	s.BlockQuote.Indent = &zero

	// Code inline - Transparent base
	s.Code.BackgroundColor = &empty
	s.Code.Color = &sapphireBright

	// Links
	s.Link.Color = &sapphireBright
	s.LinkText.Color = &copperLight

	// Horizontal Rule
	s.HorizontalRule.Color = &titaniumLight

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

	statusIcon := agentIconStyle.Render("━ ")
	statusText := agentStyle.Render("System Ready | 🛡️ Air-Gap Active (Zero Egress Perimeter)")
	if m.IsolationMode == "local_lan" {
		statusText = agentStyle.Render("System Ready | 🌐 Local LAN Perimeter")
	} else if m.IsolationMode == "hybrid" {
		statusText = agentStyle.Render("System Ready | ⚡ Hybrid Mode (HITL Shield)")
	}
	if m.IsPlanning {
		statusIcon = lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("━ ")
		statusText = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("Vraxter is Planning Strategy...")
	} else if m.IsStreaming {
		statusIcon = lipgloss.NewStyle().Foreground(colorLedAmber).Bold(true).Render("━ ")
		statusText = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("Vraxter is Thinking...")
	}
	statusBlock := statusIcon + statusText

	// Left Side: Identity
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
		"",
		statusBlock,
	)

	mascotHeight := lipgloss.Height(mascot)
	centeredInfo := lipgloss.NewStyle().
		Height(mascotHeight).
		AlignVertical(lipgloss.Center).
		Render(infoPanel)

	leftSegment := lipgloss.JoinHorizontal(lipgloss.Top,
		mascot,
		"   ",
		centeredInfo,
	)

	// Vertical separator matching mascotHeight without uncolored margins
	var sepRows []string
	sepLine := lipgloss.NewStyle().Foreground(colorCopperLine).Render(" │  ")
	for i := 0; i < mascotHeight; i++ {
		sepRows = append(sepRows, sepLine)
	}
	separator := strings.Join(sepRows, "\n")

	panelWidth := m.width - 2
	if panelWidth < 40 {
		panelWidth = 40
	}
	contentWidth := panelWidth - 6 // 2 border + 4 horizontal padding
	sepWidth := lipgloss.Width(sepLine)
	leftWidth := (contentWidth - sepWidth) / 2
	rightWidth := contentWidth - sepWidth - leftWidth

	// Right Side: Tips & Active Routing (.tui-wh-right in CSS)
	kickerStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)
	cmdCodeStyle := lipgloss.NewStyle().Foreground(colorCopperSpecular)
	labelIvory := lipgloss.NewStyle().Foreground(colorIvory)

	activeColor := colorLedEmerald
	if strings.Contains(m.ActiveModel, "(FALLBACK)") {
		activeColor = colorLedAmber
	}
	activeLine := labelIvory.Render("Active: ") + lipgloss.NewStyle().Foreground(activeColor).Bold(true).Render(m.ActiveModel)
	failoverLabel := m.FailoverModel
	if failoverLabel == "" {
		failoverLabel = "none"
	}
	failoverBadge := lipgloss.NewStyle().Foreground(colorCopperLight).Render(fmt.Sprintf("[Failover: %s]", failoverLabel))

	rawCombined := fmt.Sprintf("Active: %s  [Failover: %s]", m.ActiveModel, failoverLabel)
	var rightSide []string
	if len(rawCombined) <= rightWidth {
		rightSide = []string{
			kickerStyle.Render("TIPS FOR GETTING STARTED & ACTIVE ROUTING"),
			faintStyle.Render("• ") + cmdCodeStyle.Render("/start") + faintStyle.Render(" to configure everything  ·  ") + cmdCodeStyle.Render("/plan <task>") + faintStyle.Render(" for autonomous mode"),
			faintStyle.Render("• Type ") + cmdCodeStyle.Render("?") + faintStyle.Render(" for quick command help  ·  ") + cmdCodeStyle.Render("@") + faintStyle.Render(" for domain specialists"),
			"",
			activeLine + "  " + failoverBadge,
		}
	} else {
		rightSide = []string{
			kickerStyle.Render("TIPS FOR GETTING STARTED & ACTIVE ROUTING"),
			faintStyle.Render("• ") + cmdCodeStyle.Render("/start") + faintStyle.Render(" to configure  ·  ") + cmdCodeStyle.Render("/plan <task>") + faintStyle.Render(" for autonomous mode"),
			faintStyle.Render("• Type ") + cmdCodeStyle.Render("?") + faintStyle.Render(" for help  ·  ") + cmdCodeStyle.Render("@") + faintStyle.Render(" for specialists"),
			activeLine,
			failoverBadge,
		}
	}

	tips := lipgloss.JoinVertical(lipgloss.Left, rightSide...)
	centeredTips := lipgloss.NewStyle().
		Height(mascotHeight).
		AlignVertical(lipgloss.Center).
		Render(tips)

	leftCol := lipgloss.NewStyle().Width(leftWidth).Align(lipgloss.Left).Render(leftSegment)
	rightCol := lipgloss.NewStyle().Width(rightWidth).Align(lipgloss.Left).Render(centeredTips)

	headerContent := lipgloss.JoinHorizontal(lipgloss.Top,
		leftCol,
		separator,
		rightCol,
	)

	return renderSolidBox(headerContent, panelWidth, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}

func (m *Model) renderMascot() string {
	// Mascot: Brushed Copper Sigil (.tui-sigil-ascii in CSS)
	outerSigilStyle := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true)

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
