// Copyright (c) 2026 PatagonicRune. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/vraxter/vraxter/internal/services"
)

type ProviderWizard struct {
	manager    *services.ProviderManager
	step       int
	inputs     []textinput.Model
	err        error
	width      int
	done       bool
	quitting   bool
	ProviderID string
}

func NewProviderWizard(mgr *services.ProviderManager) *ProviderWizard {
	inputs := make([]textinput.Model, 4)

	inputs[0] = textinput.New()
	inputs[0].Placeholder = "Provider Name (e.g. My Google Account)"
	inputs[0].Focus()
	inputs[0].CharLimit = 64
	inputs[0].SetWidth(64)

	inputs[1] = textinput.New()
	inputs[1].Placeholder = "Type (google, openai, anthropic, ollama, custom)"
	inputs[1].CharLimit = 32
	inputs[1].SetWidth(32)

	inputs[2] = textinput.New()
	inputs[2].Placeholder = "API Key"
	inputs[2].EchoMode = textinput.EchoPassword
	inputs[2].EchoCharacter = '◈'
	inputs[2].CharLimit = 256
	inputs[2].SetWidth(64)

	inputs[3] = textinput.New()
	inputs[3].Placeholder = "Base URL (Optional, defaults to official for cloud)"
	inputs[3].CharLimit = 256
	inputs[3].SetWidth(64)

	return &ProviderWizard{
		manager: mgr,
		inputs:  inputs,
	}
}

func (m *ProviderWizard) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ProviderWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			m.quitting = true
			m.done = true
			return m, nil
		case "enter":
			// Special handling for Ollama - skip API Key
			if m.step == 1 && strings.ToLower(m.inputs[1].Value()) == "ollama" {
				m.step = 3
				m.inputs[3].Focus()
				return m, nil
			}

			if m.step == len(m.inputs)-1 {
				// Finalize
				name := m.inputs[0].Value()
				pType := strings.ToLower(m.inputs[1].Value())
				apiKey := m.inputs[2].Value()
				baseURL := m.inputs[3].Value()

				id, err := m.manager.AddProvider(context.Background(), name, pType, apiKey, baseURL)
				if err != nil {
					m.err = err
					return m, nil
				}
				m.ProviderID = id
				m.done = true
				return m, nil
			}

			m.step++
			m.inputs[m.step].Focus()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.inputs[m.step], cmd = m.inputs[m.step].Update(msg)
	return m, cmd
}

func (m *ProviderWizard) View() tea.View {
	if m.quitting {
		return tea.NewView("Setup cancelled.")
	}
	if m.done {
		return tea.NewView(fmt.Sprintf("✅ Provider configured successfully (ID: %s)", m.ProviderID))
	}

	var s strings.Builder

	width := m.width
	if width <= 0 {
		width = 80
	}

	headerLeft := setupTitleStyle.Render("◈ PROVIDER CONFIGURATION")
	headerRight := setupFaintStyle.Render(fmt.Sprintf("%d / %d", m.step+1, len(m.inputs)))

	pad := width - 6 - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if pad < 1 {
		pad = 1
	}
	s.WriteString(headerLeft + strings.Repeat(" ", pad) + headerRight + "\n")
	ruleWidth := width - 6
	if ruleWidth < 0 {
		ruleWidth = 0
	}
	s.WriteString(setupFaintStyle.Render(strings.Repeat("─", ruleWidth)) + "\n\n")

	steps := []string{"Identity", "Selection", "Credentials", "Endpoint"}
	// Progress
	for i := 0; i < len(steps); i++ {
		dot := "○ "
		if i == m.step {
			dot = "● "
		} else if i < m.step {
			dot = "✓ "
		}

		style := setupFaintStyle
		if i == m.step {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#d4af37")).Bold(true)
		} else if i < m.step {
			style = setupDoneStyle
		}

		s.WriteString(style.Render(dot + steps[i]))
		if i < len(steps)-1 {
			s.WriteString("  ")
		}
	}
	s.WriteString("\n\n")

	// Help Text area
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true)
	helpText := ""
	switch m.step {
	case 1:
		helpText = "Vraxter supports Google, OpenAI, Anthropic, Ollama, and custom endpoints."
	case 2:
		pType := strings.ToLower(m.inputs[1].Value())
		switch pType {
		case "google":
			helpText = "Get your API Key at: https://aistudio.google.com/app/apikey"
		case "openai":
			helpText = "Get your API Key at: https://platform.openai.com/api-keys"
		case "anthropic":
			helpText = "Get your API Key at: https://console.anthropic.com/settings/keys"
		case "ollama":
			helpText = "Ollama traditionally runs locally; no API Key is required."
		case "custom":
			helpText = "Used for custom OpenAI-compatible endpoints. No API Key usually required."
		}
	case 3:
		helpText = "Leave empty to use official APIs, or specify a custom proxy/endpoint."
	}

	if helpText != "" {
		s.WriteString(helpStyle.Render("💡 ") + helpStyle.Render(helpText) + "\n\n")
	}

	// Input
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8")).Bold(true)
	s.WriteString(fmt.Sprintf("%s\n", labelStyle.Render(steps[m.step])))
	s.WriteString(m.inputs[m.step].View() + "\n")

	if m.err != nil {
		s.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252")).Render("❌ Error: "+m.err.Error()) + "\n")
	}

	s.WriteString("\n" + setupFaintStyle.Render("(Enter: Next | Esc: Cancel)"))

	return tea.NewView(lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a2a2a")).
		Padding(1, 2).
		Width(width).
		Render(s.String()))
}
