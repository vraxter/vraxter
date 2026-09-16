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
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

type ModelWizardStep int

const (
	StepProviderSelect ModelWizardStep = iota
	StepModelSelect
	StepConfigSelect
	StepFinalizing
)

type ModelWizard struct {
	providerMgr *services.ProviderManager
	modelMgr    *services.ModelManager
	step        ModelWizardStep
	
	providers   []db.Provider
	selProvider int
	
	models      []interfaces.ModelMetadata
	selModel    int
	
	usageInput  textinput.Model
	err         error
	width       int
	done        bool
	quitting    bool
	ModelID     string
}

func NewModelWizard(pMgr *services.ProviderManager, mMgr *services.ModelManager) *ModelWizard {
	ti := textinput.New()
	ti.Placeholder = "Alias (e.g. My Default Claude)"
	ti.Focus()
	ti.CharLimit = 64
	ti.SetWidth(64)

	return &ModelWizard{
		providerMgr: pMgr,
		modelMgr:    mMgr,
		usageInput:  ti,
	}
}

func (m *ModelWizard) Init() tea.Cmd {
	return func() tea.Msg {
		providers, _ := m.providerMgr.ListProviders()
		return providers
	}
}

func (m *ModelWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []db.Provider:
		m.providers = msg
		return m, nil
	case []interfaces.ModelMetadata:
		m.models = msg
		m.step = StepModelSelect
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			m.quitting = true
			m.done = true
			return m, nil
		case "up":
			if m.step == StepProviderSelect && m.selProvider > 0 {
				m.selProvider--
			} else if m.step == StepModelSelect && m.selModel > 0 {
				m.selModel--
			}
		case "down":
			if m.step == StepProviderSelect && m.selProvider < len(m.providers)-1 {
				m.selProvider++
			} else if m.step == StepModelSelect && m.selModel < len(m.models)-1 {
				m.selModel++
			}
		case "enter":
			switch m.step {
			case StepProviderSelect:
				if len(m.providers) == 0 {
					return m, nil
				}
				p := m.providers[m.selProvider]
				return m, func() tea.Msg {
					models, _ := m.providerMgr.DiscoverModels(context.Background(), p.ID)
					return models
				}
			case StepModelSelect:
				if len(m.models) == 0 {
					return m, nil
				}
				m.step = StepConfigSelect
				m.usageInput.Focus()
				return m, textinput.Blink
			case StepConfigSelect:
				p := m.providers[m.selProvider]
				target := m.models[m.selModel]
				alias := m.usageInput.Value()
				
				id, err := m.modelMgr.AddModel(context.Background(), p.ID, target.ID, 0, alias)
				if err != nil {
					m.err = err
					return m, nil
				}
				m.ModelID = id
				m.done = true
				return m, nil
			}
		}
	}

	if m.step == StepConfigSelect {
		var cmd tea.Cmd
		m.usageInput, cmd = m.usageInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *ModelWizard) View() tea.View {
	if m.quitting {
		return tea.NewView("Setup cancelled.")
	}
	if m.done {
		return tea.NewView(fmt.Sprintf("✅ Model configured successfully (ID: %s)", m.ModelID))
	}

	var s strings.Builder

	width := m.width
	if width <= 0 {
		width = 80
	}

	headerLeft := setupTitleStyle.Render("◈ MODEL CONFIGURATION")
	headerRight := setupFaintStyle.Render(fmt.Sprintf("%d / 3", int(m.step)+1))

	pad := width - 6 - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if pad < 1 {
		pad = 1
	}
	s.WriteString(headerLeft + strings.Repeat(" ", pad) + headerRight + "\n")
	ruleWidth := width - 6
	if ruleWidth < 0 { ruleWidth = 0 }
	s.WriteString(setupFaintStyle.Render(strings.Repeat("─", ruleWidth)) + "\n\n")

	// Logic continues...
	
	switch m.step {
	case StepProviderSelect:
		s.WriteString(lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render("Step 1: Select Provider") + "\n")
		s.WriteString(setupFaintStyle.Render("Choose the provider to discover models from:") + "\n\n")
		
		if len(m.providers) == 0 {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252")).Render(" ! No providers configured. Add one first using /providers setup.") + "\n")
		}

		for i, p := range m.providers {
			marker := "  "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8"))
			if i == m.selProvider {
				marker = "❯ "
				style = lipgloss.NewStyle().Foreground(colorGold).Bold(true)
			}
			s.WriteString(style.Render(fmt.Sprintf("%s %s (%s)", marker, p.Name, p.Type)) + "\n")
		}

	case StepModelSelect:
		s.WriteString(lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render("Step 2: Select Model") + "\n")
		p := m.providers[m.selProvider]
		s.WriteString(setupFaintStyle.Render(fmt.Sprintf("Discovered from %s:", p.Name)) + "\n\n")
		
		if len(m.models) == 0 {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252")).Render(" ! No models found. Check your API Key/Endpoint.") + "\n")
		}

		for i, mod := range m.models {
			marker := "  "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8"))
			if i == m.selModel {
				marker = "❯ "
				style = lipgloss.NewStyle().Foreground(colorGold).Bold(true)
			}
			
			// Simple list for now, keeping it clean
			s.WriteString(style.Render(fmt.Sprintf("%s %-30s", marker, mod.ID)))
			if mod.ContextWindow > 0 {
				s.WriteString(setupFaintStyle.Render(fmt.Sprintf(" [%dk ctx]", mod.ContextWindow/1000)))
			}
			s.WriteString("\n")
		}

	case StepConfigSelect:
		s.WriteString(lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render("Step 3: Define Usage") + "\n")
		mod := m.models[m.selModel]
		s.WriteString(setupFaintStyle.Render(fmt.Sprintf("Registering %s...", mod.ID)) + "\n\n")
		
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8")).Bold(true)
		s.WriteString(labelStyle.Render("Assign an Alias/Friendly Name") + "\n")
		s.WriteString(m.usageInput.View() + "\n")
		
		s.WriteString("\n" + setupFaintStyle.Italic(true).Render("💡 This alias will be used to select the model in conversation."))
	}

	if m.err != nil {
		s.WriteString("\n\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252")).Render("❌ Error: "+m.err.Error()) + "\n")
	}

	s.WriteString("\n\n" + setupFaintStyle.Render("(↑/↓: Navigate | Enter: Select | Esc: Cancel)"))

	return tea.NewView(lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a2a2a")).
		Padding(1, 2).
		Width(width).
		Render(s.String()))
}
