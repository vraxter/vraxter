// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/vraxter/vraxter/internal/services"
)

type StartupWizardState int

const (
	StateSplash StartupWizardState = iota
	StateIsolationMode
	StateUserProfile
	StateProviderConfig
	StateModelRegister
	StateFinished
)

type StartupWizard struct {
	state  StartupWizardState
	width  int
	height int

	// Sub-Wizards
	userWizard  *UserWizardModel
	provWizard  *ProviderWizard
	modelWizard *ModelWizard

	// Dependencies for child creation
	provMgr  *services.ProviderManager
	modelMgr *services.ModelManager

	SelectedIsolationMode string

	quitting bool
	Done     bool
}

func NewStartupWizard(pMgr *services.ProviderManager, mMgr *services.ModelManager) *StartupWizard {
	return &StartupWizard{
		state:                 StateSplash,
		userWizard:            NewUserWizardModel(),
		provWizard:            NewProviderWizard(pMgr),
		modelWizard:           NewModelWizard(pMgr, mMgr),
		provMgr:               pMgr,
		modelMgr:              mMgr,
		SelectedIsolationMode: "airgap",
	}
}

func (m *StartupWizard) Init() tea.Cmd {
	return nil
}

func (m *StartupWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.userWizard.Width = msg.Width
		m.provWizard.width = msg.Width
		m.modelWizard.width = msg.Width
	case tea.KeyPressMsg:
		if m.state == StateSplash {
			if msg.String() == "enter" {
				m.state = StateIsolationMode
				return m, nil
			}
			if msg.String() == "esc" {
				m.quitting = true
				m.Done = true
				return m, nil
			}
		}

		if m.state == StateIsolationMode {
			switch msg.String() {
			case "shift+tab", "ctrl+b", "alt+left", "backspace":
				m.state = StateSplash
				return m, nil
			case "a", "A", "enter":
				m.SelectedIsolationMode = "airgap"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "l", "L":
				m.SelectedIsolationMode = "local_lan"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "h", "H":
				m.SelectedIsolationMode = "hybrid"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "esc":
				m.quitting = true
				m.Done = true
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	switch m.state {
	case StateUserProfile:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "shift+tab", "ctrl+b", "alt+left":
				if m.userWizard.step == 0 {
					m.state = StateIsolationMode
					return m, nil
				}
			}
		}
		var sub tea.Model
		sub, cmd = m.userWizard.Update(msg)
		m.userWizard = sub.(*UserWizardModel)
		if m.userWizard.Done {
			m.state = StateProviderConfig
			return m, m.provWizard.Init()
		}
	case StateProviderConfig:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "shift+tab", "ctrl+b", "alt+left":
				if m.provWizard.step == 0 {
					m.userWizard.Done = false
					m.userWizard.step = len(m.userWizard.inputs) - 1
					m.userWizard.inputs[m.userWizard.step].Focus()
					m.state = StateUserProfile
					return m, nil
				}
			}
		}
		var sub tea.Model
		sub, cmd = m.provWizard.Update(msg)
		m.provWizard = sub.(*ProviderWizard)
		if m.provWizard.done {
			// If cancelled, quit whole wizard
			if m.provWizard.quitting {
				m.quitting = true
				m.Done = true
				return m, nil
			}
			m.state = StateModelRegister
			return m, m.modelWizard.Init()
		}
	case StateModelRegister:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "shift+tab", "ctrl+b", "alt+left", "backspace":
				if m.modelWizard.step == StepProviderSelect {
					m.provWizard.done = false
					m.provWizard.step = len(m.provWizard.inputs) - 1
					m.provWizard.inputs[m.provWizard.step].Focus()
					m.state = StateProviderConfig
					return m, nil
				}
			}
		}
		var sub tea.Model
		sub, cmd = m.modelWizard.Update(msg)
		m.modelWizard = sub.(*ModelWizard)
		if m.modelWizard.done {
			// If cancelled, quit whole wizard
			if m.modelWizard.quitting {
				m.quitting = true
				m.Done = true
				return m, nil
			}
			m.state = StateFinished
		}
	case StateFinished:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "enter":
				m.Done = true
				return m, nil
			case "shift+tab", "ctrl+b", "alt+left", "backspace":
				m.modelWizard.done = false
				m.modelWizard.step = StepConfigSelect
				m.modelWizard.usageInput.Focus()
				m.state = StateModelRegister
				return m, nil
			}
		}
	}

	return m, cmd
}

func (m *StartupWizard) View() tea.View {
	if m.quitting {
		return tea.NewView("Initialization cancelled. Run 'vraxter' when ready to configure the enclave.")
	}

	var content string
	switch m.state {
	case StateSplash:
		content = m.renderSplash()
	case StateIsolationMode:
		content = m.renderIsolationSelector()
	case StateUserProfile:
		content = m.userWizard.View().Content
	case StateProviderConfig:
		content = m.provWizard.View().Content
	case StateModelRegister:
		content = m.modelWizard.View().Content
	case StateFinished:
		content = m.renderFinished()
	}
	return tea.NewView(content)
}

func (m *StartupWizard) renderSplash() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("    ◈ VRAXTER SOVEREIGN RUNTIME — ENCLAVE INITIALIZATION") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("    Zero-egress autonomous execution runtime for critical environments.") + "\n\n")

	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Foreground(colorStone).Width(width - 8).Render(
		"This setup configures the local enclave:\n\n" +
			" 1. Select Network Isolation Mode (airgap / local_lan / hybrid)\n" +
			" 2. Configure Operator Profile\n" +
			" 3. Connect an Inference Provider (Ollama, vLLM, etc.)\n" +
			" 4. Register an Operational Model\n",
	))

	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorCopperLight).Render("Press ENTER to configure enclave..."))

	return renderSolidBox(s.String(), width, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}

func (m *StartupWizard) renderFinished() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(setupDoneStyle.GetForeground()).Bold(true).Render("    ◈ ENCLAVE INITIALIZED") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("    Vraxter runtime policies and operator settings are active.") + "\n\n")

	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Foreground(colorStone).Width(width - 8).Render(
		"Runtime security policies (isolation mode, encrypted SQLite, audit ledger)\n" +
			"are enforced on all subsequent sessions.\n\n" +
			"Manage configuration later with:\n" +
			" /user setup      /providers setup      /models setup",
	))

	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorCopperLight).Render("Press ENTER to enter the operator console..."))

	return renderSolidBox(s.String(), width, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}

func (m *StartupWizard) renderIsolationSelector() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("    ◈ SELECT ENCLAVE ISOLATION MODE") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("    Controls network perimeter enforcement and provider egress policy.") + "\n\n")

	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Foreground(colorStone).Width(width - 8).Render(
		"Select network isolation posture:\n\n" +
			" [a] Airgap      (Recommended — Zero-egress, loopback-only inference, strict_local)\n" +
			" [l] Local LAN   (Private RFC 1918 intranet endpoints only, zero public internet)\n" +
			" [h] Hybrid      (Controlled egress to explicitly configured endpoints)\n",
	))

	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorCopperLight).Render("Press [a], [l], or [h] (ENTER defaults to airgap)..."))

	return renderSolidBox(s.String(), width, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}
