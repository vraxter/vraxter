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
	StateImplementation
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

	SelectedImplementation string

	quitting bool
	Done     bool
}

func NewStartupWizard(pMgr *services.ProviderManager, mMgr *services.ModelManager) *StartupWizard {
	return &StartupWizard{
		state:       StateSplash,
		userWizard:  NewUserWizardModel(),
		provWizard:  NewProviderWizard(pMgr),
		modelWizard: NewModelWizard(pMgr, mMgr),
		provMgr:     pMgr,
		modelMgr:    mMgr,
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
				m.state = StateImplementation
				return m, nil
			}
			if msg.String() == "esc" {
				m.quitting = true
				m.Done = true
				return m, nil
			}
		}
		
		if m.state == StateImplementation {
			switch msg.String() {
			case "shift+tab", "ctrl+b", "alt+left", "backspace":
				m.state = StateSplash
				return m, nil
			case "h", "H":
				m.SelectedImplementation = "habitat"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "f", "F":
				m.SelectedImplementation = "facility"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "e", "E":
				m.SelectedImplementation = "enterprise"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "s", "S":
				m.SelectedImplementation = "sovereign"
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			case "c", "C":
				m.SelectedImplementation = "core"
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
					m.state = StateImplementation
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
		return tea.NewView("Onboarding cancelled. Vraxter is ready when you are.")
	}

	var content string
	switch m.state {
	case StateSplash:
		content = m.renderSplash()
	case StateImplementation:
		content = m.renderImplementationSelector()
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
	s.WriteString(lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("    ◈ WELCOME TO VRAXTER") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("    The autonomous workstation for the modern engineer.") + "\n\n")

	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Foreground(colorStone).Width(width - 8).Render(
		"This wizard will guide you through the initial configuration:\n\n" +
			" 1. Create your Identity\n" +
			" 2. Connect a Provider (OpenAI, Google, etc.)\n" +
			" 3. Register your first Model\n",
	))

	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorCopperLight).Render("Press ENTER to begin your journey..."))

	return renderSolidBox(s.String(), width, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}

func (m *StartupWizard) renderFinished() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(setupDoneStyle.GetForeground()).Bold(true).Render("    ◈ CONFIGURATION COMPLETE") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("    Vraxter has been initialized and is ready for action.") + "\n\n")

	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Foreground(colorStone).Width(width - 8).Render(
		"You can now start chatting with your models. Simply type your request \n" +
			"in the console after exiting this wizard.\n\n" +
			"To modify these settings later, use:\n" +
			" /user setup      /providers setup      /models setup",
	))

	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorCopperLight).Render("Press ENTER to enter the workstation..."))

	return renderSolidBox(s.String(), width, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}

func (m *StartupWizard) renderImplementationSelector() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("    ◈ SELECT IMPLEMENTATION WORLD") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("    Vraxter activates different powers based on your environment.") + "\n\n")

	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Foreground(colorStone).Width(width - 8).Render(
		"Please select your implementation type:\n\n" +
			" [c] Core        (Engineering Workstation & Local Skills)\n" +
			" [h] Habitat     (Spatial Awareness & IoT Routing)\n" +
			" [f] Facility    (Building Management Systems)\n" +
			" [e] Enterprise  (Air-Gapped Telemetry, RBAC)\n" +
			" [s] Sovereign   (All Powers Unlocked)\n",
	))

	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorCopperLight).Render("Press the corresponding key to select..."))

	return renderSolidBox(s.String(), width, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
}
