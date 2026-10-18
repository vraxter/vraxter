package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/pkg/types"
)

func assertMaxSGRParams(t *testing.T, label, s string) {
	t.Helper()
	matches := sgrSeqRe.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		if m[1] == "" {
			continue
		}
		parts := strings.Split(m[1], ";")
		if len(parts) > 8 {
			t.Errorf("%s has SGR sequence with %d params (> 8, ansi limit is 32): %q", label, len(parts), m[0])
		}
	}
	// Also run lipgloss.Width on every line, which exercises charmbracelet/x/ansi.DecodeSequenceWc
	for i, line := range strings.Split(s, "\n") {
		_ = i
		_ = lipgloss.Width(line)
	}
}

func TestSigilSpinnerStaticDimensions(t *testing.T) {
	frames := buildSigilSpinnerFrames()
	if len(frames) != 10 {
		t.Fatalf("expected 10 sigil spinner frames, got %d", len(frames))
	}
	for i, f := range frames {
		lines := strings.Split(f, "\n")
		if len(lines) != 2 {
			t.Fatalf("frame %d has %d lines, want 2: %q", i, len(lines), f)
		}
		for r, line := range lines {
			if w := lipgloss.Width(line); w != 4 {
				t.Errorf("frame %d row %d visual width = %d, want 4: %q", i, r, w, line)
			}
		}
	}
}

func TestZeroBleedSurfaceRendering(t *testing.T) {
	inner := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("◈ VRAXTER") +
		"  " +
		lipgloss.NewStyle().Foreground(colorAsh).Render("Session: test-123")

	box := renderSolidBox(inner, 60, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
	assertMaxSGRParams(t, "renderSolidBox", box)
	lines := strings.Split(box, "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}

	bgSeq := ansiBg(hexHeaderBg)
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 60 {
			t.Errorf("line %d width = %d, want 60", i, w)
		}
		trimmed := strings.TrimSuffix(line, "\x1b[0m")
		if strings.Contains(trimmed, "\x1b[0m") || strings.Contains(trimmed, "\x1b[m") {
			t.Errorf("line %d contains mid-line bare ANSI reset that would bleed background: %q", i, trimmed)
		}
		if !strings.Contains(line, bgSeq) {
			t.Errorf("line %d missing surface background SGR %q", i, bgSeq)
		}
	}
}

func TestLeftAccentBlockNoBleed(t *testing.T) {
	inlineCmd := lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("/models")
	content := userStyle.Render("drodriguez") + "\n" +
		"Hola " + inlineCmd + " mundo"

	block := strings.TrimSuffix(renderLeftAccentBlock(content, 50, 1, 2, hexUserBlockBg, hexIvory, hexStone), "\n")
	assertMaxSGRParams(t, "renderLeftAccentBlock", block)
	lines := strings.Split(block, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	bgSeq := ansiBg(hexUserBlockBg)
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 50 {
			t.Errorf("line %d width = %d, want 50", i, w)
		}
		trimmed := strings.TrimSuffix(line, "\x1b[0m")
		if strings.Contains(trimmed, "\x1b[0m") || strings.Contains(trimmed, "\x1b[m") {
			t.Errorf("line %d contains mid-line bare ANSI reset: %q", i, trimmed)
		}
		if !strings.Contains(line, bgSeq) {
			t.Errorf("line %d missing surface background SGR %q", i, bgSeq)
		}
	}
}

func TestFullViewAndPlanNoANSIPanic(t *testing.T) {
	s := spinner.New()
	s.Spinner = sigilSpinner
	s.Style = lipgloss.NewStyle()

	ta := textarea.New()
	ta.SetWidth(80)
	ta.SetHeight(1)

	vp := viewport.New()
	vp.SetWidth(106)
	vp.SetHeight(20)

	r, err := buildRenderer(110)
	if err != nil {
		t.Fatalf("buildRenderer failed: %v", err)
	}

	m := &Model{
		width:        110,
		height:       36,
		Ready:        true,
		Input:        ta,
		Viewport:     vp,
		renderer:     r,
		SessionID:    "0f8a92c1-44b1-48f3-8a11-90c82e771b2e",
		CurrentFocus: "vraxter",
		Spinner:      s,
		IsPlanning:   true,
		IsStreaming:  true,
		ActiveJobs: []*v1.JobStatus{
			{Id: "job-1", Title: "Architecture Planner", Status: "running"},
		},
		Autocomplete: AutocompleteState{
			IsActive: true,
			Type:     "specialist",
			Items:    []string{"@architect", "@coder", "@researcher"},
			Cursor:   0,
		},
	}

	planJSON := `{"narrative":"Verify zero background bleed","goal":"Test TUI surfaces","reasoning":"Ensure sub-cards and phases render cleanly","estimations":"2m","phases":[{"title":"Header","description":"Check header surface","tasks":["Verify border","Verify padding"],"specialist_id":"coder"}]}`
	m.History = []HistoryBlock{
		{Type: BlockMarkdown, Sender: SenderUser, Content: "/plan test"},
		{Type: BlockComponent, Content: m.renderPlan(planJSON, 110, "") + "\n"},
		{Type: BlockComponent, Content: m.renderSkillApproval("quarantine.wasm", `{"skill_id":"quarantine.wasm","params":{"cidr":"10.9.4.0/24"}}`, 110, "") + "\n"},
		{Type: BlockComponent, Content: m.renderExecutionBanner("completed", "Plan Execution", "All phases completed", "STRICT_LOCAL VERIFIED", 110) + "\n"},
	}
	m.Viewport.SetContent(m.renderMarkdown(m.History))

	// Render full View in Planning state with ActiveJobs pill
	v1View := m.View()
	assertMaxSGRParams(t, "View(Planning+ActiveJobs)", v1View.Content)

	// Render full View in WaitingForApproval Decision Bridge state
	m.IsPlanning = false
	m.IsStreaming = false
	m.IsWaitingForApproval = true
	v2View := m.View()
	assertMaxSGRParams(t, "View(WaitingForApproval)", v2View.Content)
}

func TestUserMessageAndSlashCommandDesign(t *testing.T) {
	r, err := buildRenderer(100)
	if err != nil {
		t.Fatalf("buildRenderer failed: %v", err)
	}
	m := &Model{
		width:    100,
		height:   36,
		renderer: r,
	}

	userMsg := m.renderUserMessage("/plan @security-auditor audit local subnet 10.9.4.0/24", 98)
	assertMaxSGRParams(t, "renderUserMessage", userMsg)
	plainUser := stripANSI(userMsg)
	if !strings.Contains(plainUser, "> /plan @security-auditor") {
		t.Errorf("expected user message to start with '> /plan @security-auditor', got %q", plainUser)
	}
	if !strings.Contains(plainUser, "YOU") {
		t.Errorf("expected user message to include right-aligned 'YOU' badge, got %q", plainUser)
	}
	if strings.Contains(plainUser, "┃") {
		t.Errorf("expected thin vertical border '│', found thick '┃' in %q", plainUser)
	}
	if !strings.Contains(plainUser, "│") {
		t.Errorf("expected thin vertical border '│' in %q", plainUser)
	}

	// Execute slash commands and verify consistent component blocks
	_ = m.handleSlashCommand("/help")
	_ = m.handleSlashCommand("/jobs")
	_ = m.handleSlashCommand("/session vrx-sec-01")
	_ = m.handleSlashCommand("/activate llama-3-70b")
	_ = m.handleSlashCommand("/unknowncmd")

	renderedStream := m.renderMarkdown(m.History)
	assertMaxSGRParams(t, "renderMarkdown(slashCommands)", renderedStream)
	plainStream := stripANSI(renderedStream)
	for _, want := range []string{
		"COMMAND OUTPUT //",
		"/help",
		"BACKGROUND DAEMON JOBS",
		"SESSION CONTEXT SWITCHED",
		"MODEL OVERRIDE LOCKED",
		"UNKNOWN COMMAND",
	} {
		if !strings.Contains(plainStream, want) {
			t.Errorf("expected slash command stream to contain %q, got:\n%s", want, plainStream)
		}
	}
}

func TestHeaderFailoverAndActiveModel(t *testing.T) {
	m := &Model{
		width:         120,
		height:        36,
		ActiveModel:   "llama-3-local (LOCKED)",
		FailoverModel: "gemini-2.5-flash",
		SessionID:     "test-sess-001",
	}

	header := m.renderHeader()
	assertMaxSGRParams(t, "renderHeader(active+failover)", header)
	plain := stripANSI(header)

	if !strings.Contains(plain, "Active: llama-3-local (LOCKED)") {
		t.Errorf("expected header to contain 'Active: llama-3-local (LOCKED)', got:\n%s", plain)
	}
	if !strings.Contains(plain, "[Failover: gemini-2.5-flash]") {
		t.Errorf("expected header to contain '[Failover: gemini-2.5-flash]', got:\n%s", plain)
	}
}

func TestTopLevelSpinnerTickerNeverBlocked(t *testing.T) {
	s := spinner.New()
	s.Spinner = sigilSpinner

	m := &Model{
		width:       100,
		height:      36,
		Spinner:     s,
		IsStreaming: true,
		Ready:       false,
	}

	// Generate matching TickMsg using s.Tick()
	tickMsg := s.Tick()

	newM, cmd := m.Update(tickMsg)
	if cmd == nil {
		t.Fatal("expected non-nil cmd from top-level spinner TickMsg to keep loop alive")
	}
	_ = newM
}

func TestResolveModelDisplayName(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vraxter-tui-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	keyPath := filepath.Join(tempDir, "test.key")
	crypto, err := security.NewCryptoService(keyPath)
	if err != nil {
		t.Fatalf("failed to create crypto: %v", err)
	}

	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	pRepo := db.NewProviderRepository(store, crypto)
	_ = pRepo.Create(&db.Provider{
		ID:       "prov-custom",
		Name:     "Custom Local",
		Type:     "custom",
		BaseURL:  "http://192.168.1.35:11434",
		IsActive: true,
	})

	mRepo := db.NewModelRepository(store, crypto)
	_ = mRepo.UpsertModel(types.ModelConfig{
		ID:         "8597e799",
		ProviderID: "prov-custom",
		Provider:   "custom",
		Model:      "qwen2.5:3b",
		Alias:      "Mini Qwen",
		Priority:   0,
		IsActive:   true,
	})

	m := &Model{
		appStore:  store,
		appCrypto: crypto,
	}

	// 1. Passing hex ID should resolve to Alias
	got := m.resolveModelDisplayName("8597e799")
	if got != "Mini Qwen" {
		t.Errorf("expected 'Mini Qwen', got %q", got)
	}

	// 2. Passing hex ID with single quotes should also resolve to Alias
	gotQuoted := m.resolveModelDisplayName("'8597e799'")
	if gotQuoted != "Mini Qwen" {
		t.Errorf("expected 'Mini Qwen' for quoted ID, got %q", gotQuoted)
	}

	// 3. Passing remote model string should also resolve to Alias
	gotModel := m.resolveModelDisplayName("qwen2.5:3b")
	if gotModel != "Mini Qwen" {
		t.Errorf("expected 'Mini Qwen' for remote model string, got %q", gotModel)
	}

	// 4. Unknown string should be returned as-is
	gotUnknown := m.resolveModelDisplayName("unknown-model")
	if gotUnknown != "unknown-model" {
		t.Errorf("expected 'unknown-model', got %q", gotUnknown)
	}
}

func TestRenderNotification(t *testing.T) {
	m := &Model{}

	tests := []struct {
		name      string
		icon      string
		title     string
		detail    string
		badge     string
		wantSub   string
		wantBadge string
	}{
		{
			name:      "Specialist Delegation",
			icon:      "⚡",
			title:     "Swarm Delegated:",
			detail:    "@security-auditor",
			badge:     "SWARM ACTIVE",
			wantSub:   "@security-auditor",
			wantBadge: "[SWARM ACTIVE]",
		},
		{
			name:      "Supervisor Resumed",
			icon:      "◈",
			title:     "Supervisor Resumed:",
			detail:    "Control returned to Vraxter",
			badge:     "VRAXTER",
			wantSub:   "Control returned to Vraxter",
			wantBadge: "[VRAXTER]",
		},
		{
			name:      "Skill Call",
			icon:      "◆",
			title:     "Running Tool:",
			detail:    "vraxter-read-file",
			badge:     "ACTIVE",
			wantSub:   "vraxter-read-file",
			wantBadge: "[ACTIVE]",
		},
		{
			name:      "Daemon Spawned",
			icon:      "⚙",
			title:     "Daemon Spawned:",
			detail:    "Architecture Planner Daemon led by @security-auditor",
			badge:     "PLANNING",
			wantSub:   "Architecture Planner",
			wantBadge: "[PLANNING]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rendered := m.renderNotification(tc.icon, tc.title, tc.detail, tc.badge)
			assertMaxSGRParams(t, tc.name, rendered)

			if !strings.Contains(rendered, tc.wantSub) {
				t.Errorf("renderNotification() = %q, want it to contain %q", rendered, tc.wantSub)
			}
			if !strings.Contains(rendered, tc.wantBadge) {
				t.Errorf("renderNotification() = %q, want it to contain badge %q", rendered, tc.wantBadge)
			}
			if !strings.HasPrefix(rendered, "  ") {
				t.Errorf("renderNotification() should start with 2-space indentation: %q", rendered)
			}
			if !strings.HasSuffix(rendered, "\n\n") {
				t.Errorf("renderNotification() should end with trailing newline: %q", rendered)
			}
		})
	}
}

func TestPlannerDaemonNotificationsAndOrdering(t *testing.T) {
	planJSON := `{"narrative":"Verify ordering","goal":"Test notification sequence","reasoning":"Ensure completion precedes plan","estimations":"1m","phases":[{"title":"Audit","description":"Run security scan","tasks":["Check bounds"],"specialist_id":"security-auditor"}]}`

	t.Run("Plan proposed before job completed arrives", func(t *testing.T) {
		m := &Model{
			width:                100,
			height:               36,
			ActiveCardHistoryIdx: -1,
		}

		// 1. JOB_STARTED
		updatedM, _ := m.Update(SystemEventMsgWrapper{
			Event: &v1.SystemEvent{
				Type:  v1.SystemEvent_JOB_STARTED,
				Title: "Architecture Planner (@SECURITY-AUDITOR)",
				JobId: "job-0a6a5467",
			},
		})
		m = updatedM.(*Model)
		if !m.IsPlanning {
			t.Errorf("Expected IsPlanning to be true after planner JOB_STARTED")
		}
		if len(m.History) != 1 {
			t.Fatalf("Expected 1 history item, got %d", len(m.History))
		}
		if !strings.Contains(m.History[0].Content, "⚙") || !strings.Contains(m.History[0].Content, "[DAEMON ACTIVE]") {
			t.Errorf("JOB_STARTED notification missing expected content: %s", m.History[0].Content)
		}

		// 2. PLAN_PROPOSED
		updatedM, _ = m.Update(SystemEventMsgWrapper{
			Event: &v1.SystemEvent{
				Type:    v1.SystemEvent_PLAN_PROPOSED,
				Payload: planJSON,
			},
		})
		m = updatedM.(*Model)
		if len(m.History) != 2 {
			t.Fatalf("Expected 2 history items, got %d", len(m.History))
		}
		if m.ActiveCardHistoryIdx != 1 {
			t.Errorf("Expected ActiveCardHistoryIdx to be 1, got %d", m.ActiveCardHistoryIdx)
		}

		// 3. JOB_COMPLETED
		updatedM, _ = m.Update(SystemEventMsgWrapper{
			Event: &v1.SystemEvent{
				Type:    v1.SystemEvent_JOB_COMPLETED,
				Title:   "Architecture Planner (@SECURITY-AUDITOR)",
				Payload: "Plan successfully generated and pushed to UI.",
			},
		})
		m = updatedM.(*Model)
		if len(m.History) != 3 {
			t.Fatalf("Expected 3 history items, got %d", len(m.History))
		}

		// Notification MUST be inserted BEFORE the plan card
		if !strings.Contains(m.History[1].Content, "✓") || !strings.Contains(m.History[1].Content, "[STRICT_LOCAL VERIFIED]") {
			t.Errorf("Expected history[1] to be the completed notification, got: %s", m.History[1].Content)
		}
		if !strings.Contains(m.History[2].Content, "PLANNING") && !strings.Contains(m.History[2].Content, "Audit") {
			t.Errorf("Expected history[2] to be the plan card, got: %s", m.History[2].Content)
		}
		if m.ActiveCardHistoryIdx != 2 {
			t.Errorf("Expected ActiveCardHistoryIdx to be shifted to 2, got %d", m.ActiveCardHistoryIdx)
		}

		// Ensure redundant message payload was never appended
		for idx, h := range m.History {
			if strings.Contains(h.Content, "Plan successfully generated and pushed to UI.") {
				t.Errorf("History block %d contained suppressed payload: %s", idx, h.Content)
			}
		}
	})

	t.Run("Job completed before plan proposed arrives", func(t *testing.T) {
		m := &Model{
			width:                100,
			height:               36,
			ActiveCardHistoryIdx: -1,
		}

		// 1. JOB_STARTED
		updatedM, _ := m.Update(SystemEventMsgWrapper{
			Event: &v1.SystemEvent{
				Type:  v1.SystemEvent_JOB_STARTED,
				Title: "Architecture Planner (@SECURITY-AUDITOR)",
				JobId: "job-0a6a5467",
			},
		})
		m = updatedM.(*Model)

		// 2. JOB_COMPLETED arrives first
		updatedM, _ = m.Update(SystemEventMsgWrapper{
			Event: &v1.SystemEvent{
				Type:    v1.SystemEvent_JOB_COMPLETED,
				Title:   "Architecture Planner (@SECURITY-AUDITOR)",
				Payload: "",
			},
		})
		m = updatedM.(*Model)
		if len(m.History) != 2 {
			t.Fatalf("Expected 2 history items, got %d", len(m.History))
		}
		if !strings.Contains(m.History[1].Content, "✓") || !strings.Contains(m.History[1].Content, "[STRICT_LOCAL VERIFIED]") {
			t.Errorf("Expected history[1] to be completion notification, got: %s", m.History[1].Content)
		}

		// 3. PLAN_PROPOSED arrives second
		updatedM, _ = m.Update(SystemEventMsgWrapper{
			Event: &v1.SystemEvent{
				Type:    v1.SystemEvent_PLAN_PROPOSED,
				Payload: planJSON,
			},
		})
		m = updatedM.(*Model)
		if len(m.History) != 3 {
			t.Fatalf("Expected 3 history items, got %d", len(m.History))
		}
		if !strings.Contains(m.History[1].Content, "✓") {
			t.Errorf("Expected history[1] to remain completion notification, got: %s", m.History[1].Content)
		}
		if m.ActiveCardHistoryIdx != 2 {
			t.Errorf("Expected ActiveCardHistoryIdx to be 2, got %d", m.ActiveCardHistoryIdx)
		}
	})
}

