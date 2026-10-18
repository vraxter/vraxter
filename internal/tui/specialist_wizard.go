// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/types"
)

type SpecialistWizardStep int

const (
	SpecStepArchetype SpecialistWizardStep = iota
	SpecStepName
	SpecStepCapabilities
	SpecStepModel
	SpecStepConfirm
)

type SpecialistArchetype struct {
	Title        string
	DefaultName  string
	DomainDesc   string
	Capabilities []string
}

type CapabilityItem struct {
	Name     string
	Selected bool
}

type SpecialistWizard struct {
	specRepo   *db.SpecialistRepository
	modelsRepo *db.ModelRepository

	step         SpecialistWizardStep
	archetypes   []SpecialistArchetype
	selArchetype int

	nameInput  textinput.Model
	descInput  textinput.Model
	customCapInput textinput.Model

	capabilities []CapabilityItem
	selCapIndex  int
	isAddingCap  bool

	models   []types.ModelConfig
	selModel int

	Result   *types.Specialist
	Done     bool
	quitting bool
	Width    int
	err      error
}

func NewSpecialistWizard(specRepo *db.SpecialistRepository, modelsRepo *db.ModelRepository) *SpecialistWizard {
	archetypes := []SpecialistArchetype{
		{
			Title:       "Cybersecurity & DevSecOps",
			DefaultName: "Security_Auditor",
			DomainDesc:  "Vulnerability assessments, security audits, threat modeling, and system hardening.",
			Capabilities: []string{
				"Vulnerability Audits & CVE Scanning",
				"Threat Modeling & Attack Surface Review",
				"Infrastructure & Network Hardening",
				"Static Code Security Analysis (SAST)",
				"Secrets & Credential Hygiene",
				"Zero-Trust Architecture & Compliance",
			},
		},
		{
			Title:       "Distributed Systems & Staff Engineer",
			DefaultName: "Staff_Engineer",
			DomainDesc:  "Large-scale architectural design, high concurrency, resilience, and database query optimization.",
			Capabilities: []string{
				"Distributed Systems & Event-Driven Architecture",
				"Concurrency, Goroutines & Async Pipelines",
				"High-Performance Database Query Tuning",
				"API Contract Governance & Protocol Design",
				"Failure Mode & Chaos Engineering Analysis",
			},
		},
		{
			Title:       "Cloud Infrastructure & SRE",
			DefaultName: "SRE_Specialist",
			DomainDesc:  "Infrastructure automation, Kubernetes orchestration, CI/CD pipelines, and observability.",
			Capabilities: []string{
				"Kubernetes & Container Orchestration",
				"Terraform / OpenTofu Infrastructure as Code",
				"CI/CD Pipeline Hardening & Performance",
				"Prometheus / Grafana Observability",
				"Disaster Recovery & Incident Runbooks",
			},
		},
		{
			Title:       "Full-Stack Web & UI/UX",
			DefaultName: "UI_Specialist",
			DomainDesc:  "Modern user interface development, accessibility, terminal & web UX, and responsive rendering.",
			Capabilities: []string{
				"Modern Frontend Component Architecture",
				"Responsive Design & Terminal UI UX",
				"API Integration & State Management",
				"Performance & Bundle Optimization",
			},
		},
		{
			Title:       "Custom Domain Specialist",
			DefaultName: "Custom_Specialist",
			DomainDesc:  "Specialist with customized domain and capabilities defined by the operator.",
			Capabilities: []string{
				"Domain Analysis & Research",
				"Technical Execution & Troubleshooting",
				"Best Practice Adherence & Guidance",
			},
		},
	}

	ni := textinput.New()
	ni.Placeholder = "Specialist Identifier (e.g. Security_Auditor)"
	ni.CharLimit = 32
	ni.SetWidth(36)
	ni.SetValue(archetypes[0].DefaultName)
	ni.Focus()

	di := textinput.New()
	di.Placeholder = "Detailed domain expertise description"
	di.CharLimit = 256
	di.SetWidth(56)
	di.SetValue(archetypes[0].DomainDesc)

	ci := textinput.New()
	ci.Placeholder = "Type extra focus & press Enter..."
	ci.CharLimit = 64
	ci.SetWidth(44)

	caps := make([]CapabilityItem, len(archetypes[0].Capabilities))
	for i, c := range archetypes[0].Capabilities {
		caps[i] = CapabilityItem{Name: c, Selected: true}
	}

	var activeModels []types.ModelConfig
	if modelsRepo != nil {
		if mList, err := modelsRepo.GetActiveModels(); err == nil {
			activeModels = mList
		}
	}

	return &SpecialistWizard{
		specRepo:     specRepo,
		modelsRepo:   modelsRepo,
		archetypes:   archetypes,
		nameInput:    ni,
		descInput:    di,
		customCapInput: ci,
		capabilities: caps,
		models:       activeModels,
	}
}

func (m *SpecialistWizard) Init() tea.Cmd {
	return textinput.Blink
}

func (m *SpecialistWizard) loadArchetype(idx int) {
	if idx < 0 || idx >= len(m.archetypes) {
		return
	}
	arch := m.archetypes[idx]
	m.nameInput.SetValue(arch.DefaultName)
	m.descInput.SetValue(arch.DomainDesc)

	m.capabilities = make([]CapabilityItem, len(arch.Capabilities))
	for i, c := range arch.Capabilities {
		m.capabilities[i] = CapabilityItem{Name: c, Selected: true}
	}
	m.selCapIndex = 0
}

func (m *SpecialistWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.quitting = true
			m.Done = true
			return m, nil

		case "shift+tab", "ctrl+b", "alt+left":
			m.err = nil
			if m.isAddingCap {
				m.isAddingCap = false
				m.customCapInput.Blur()
				return m, nil
			}
			if m.step > SpecStepArchetype {
				m.step--
				if m.step == SpecStepName {
					m.nameInput.Focus()
				}
				return m, nil
			}

		case "up", "k":
			switch m.step {
			case SpecStepArchetype:
				if m.selArchetype > 0 {
					m.selArchetype--
					m.loadArchetype(m.selArchetype)
				}
			case SpecStepCapabilities:
				if !m.isAddingCap && m.selCapIndex > 0 {
					m.selCapIndex--
				}
			case SpecStepModel:
				if m.selModel > 0 {
					m.selModel--
				}
			}
			return m, nil

		case "down", "j":
			switch m.step {
			case SpecStepArchetype:
				if m.selArchetype < len(m.archetypes)-1 {
					m.selArchetype++
					m.loadArchetype(m.selArchetype)
				}
			case SpecStepCapabilities:
				if !m.isAddingCap && m.selCapIndex < len(m.capabilities)-1 {
					m.selCapIndex++
				}
			case SpecStepModel:
				total := len(m.models) // 0 is default engine model, 1..len are specific
				if m.selModel < total {
					m.selModel++
				}
			}
			return m, nil

		case " ":
			if m.step == SpecStepCapabilities && !m.isAddingCap {
				if m.selCapIndex >= 0 && m.selCapIndex < len(m.capabilities) {
					m.capabilities[m.selCapIndex].Selected = !m.capabilities[m.selCapIndex].Selected
				}
				return m, nil
			}

		case "+", "a":
			if m.step == SpecStepCapabilities && !m.isAddingCap {
				m.isAddingCap = true
				m.customCapInput.SetValue("")
				m.customCapInput.Focus()
				return m, textinput.Blink
			}

		case "enter":
			m.err = nil
			switch m.step {
			case SpecStepArchetype:
				m.step = SpecStepName
				m.nameInput.Focus()
				return m, nil

			case SpecStepName:
				name := strings.TrimSpace(m.nameInput.Value())
				if name == "" {
					m.err = fmt.Errorf("name cannot be empty")
					return m, nil
				}
				// Sanitize name: no spaces, only alphanumeric and underscores
				cleanName := strings.ReplaceAll(name, " ", "_")
				m.nameInput.SetValue(cleanName)
				m.step = SpecStepCapabilities
				m.nameInput.Blur()
				return m, nil

			case SpecStepCapabilities:
				if m.isAddingCap {
					val := strings.TrimSpace(m.customCapInput.Value())
					if val != "" {
						m.capabilities = append(m.capabilities, CapabilityItem{Name: val, Selected: true})
						m.selCapIndex = len(m.capabilities) - 1
					}
					m.isAddingCap = false
					m.customCapInput.Blur()
					return m, nil
				}
				m.step = SpecStepModel
				return m, nil

			case SpecStepModel:
				m.step = SpecStepConfirm
				return m, nil

			case SpecStepConfirm:
				// Build the final specialist
				name := strings.TrimSpace(m.nameInput.Value())
				desc := strings.TrimSpace(m.descInput.Value())

				var selectedCaps []string
				for _, c := range m.capabilities {
					if c.Selected {
						selectedCaps = append(selectedCaps, c.Name)
					}
				}

				expertise := desc
				if len(selectedCaps) > 0 {
					expertise += fmt.Sprintf(" Focus areas and capabilities: %s.", strings.Join(selectedCaps, ", "))
				}

				var boundModelID string
				if m.selModel > 0 && m.selModel-1 < len(m.models) {
					boundModelID = m.models[m.selModel-1].ID
				}

				sysPrompt := fmt.Sprintf("Act as an elite specialist in %s. Respond with rigorous technical criteria. Your key capabilities include: %s. Strictly adhere to Vraxter protocol constraints.",
					desc, strings.Join(selectedCaps, "; "))

				spec := types.Specialist{
					ID:           uuid.New().String(),
					Name:         name,
					Expertise:    expertise,
					ModelID:      boundModelID,
					SystemPrompt: sysPrompt,
					CreatedAt:    time.Now(),
				}

				if m.specRepo != nil {
					if err := m.specRepo.CreateSpecialist(spec); err != nil {
						m.err = fmt.Errorf("failed to save specialist: %w", err)
						return m, nil
					}
				}

				m.Result = &spec
				m.Done = true
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	if m.step == SpecStepName {
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	if m.step == SpecStepCapabilities && m.isAddingCap {
		m.customCapInput, cmd = m.customCapInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *SpecialistWizard) View() tea.View {
	w := m.Width
	if w <= 0 {
		w = 90
	}
	panelWidth := w - 4
	if panelWidth > 86 {
		panelWidth = 86
	}
	if panelWidth < 50 {
		panelWidth = 50
	}

	titleKicker := lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("◈ SPECIALIST ARCHITECTURE WIZARD")
	stepperLine := m.renderStepper(panelWidth)

	var bodyContent string

	switch m.step {
	case SpecStepArchetype:
		bodyContent = m.renderArchetypeSelect()
	case SpecStepName:
		bodyContent = m.renderNameStep()
	case SpecStepCapabilities:
		bodyContent = m.renderCapabilitiesStep()
	case SpecStepModel:
		bodyContent = m.renderModelStep()
	case SpecStepConfirm:
		bodyContent = m.renderConfirmStep()
	}

	errorBar := ""
	if m.err != nil {
		errorBar = "\n" + lipgloss.NewStyle().Foreground(colorLedCrimson).Bold(true).Render(fmt.Sprintf("❌ %v", m.err))
	}

	footerHelp := lipgloss.NewStyle().Foreground(colorAsh).Render(
		"↑/↓: Navigate  ·  Enter: Next  ·  Ctrl+B: Back  ·  Esc: Cancel",
	)

	fullContent := titleKicker + "\n" +
		stepperLine + "\n\n" +
		bodyContent +
		errorBar + "\n\n" +
		footerHelp

	box := renderSolidBox(fullContent, panelWidth, 1, 2, hexHeaderBg, hexIvory, lipgloss.NormalBorder(), hexCopper)
	v := tea.NewView(box)
	v.AltScreen = true
	return v
}

func (m *SpecialistWizard) renderStepper(width int) string {
	steps := []string{"1. Archetype", "2. Identity", "3. Focus & Caps", "4. Model", "5. Vault"}
	var rendered []string
	for i, s := range steps {
		if SpecialistWizardStep(i) == m.step {
			rendered = append(rendered, lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("["+s+"]"))
		} else if SpecialistWizardStep(i) < m.step {
			rendered = append(rendered, lipgloss.NewStyle().Foreground(colorLedEmerald).Render("✓ "+s))
		} else {
			rendered = append(rendered, lipgloss.NewStyle().Foreground(colorAsh).Render(s))
		}
	}
	return strings.Join(rendered, lipgloss.NewStyle().Foreground(colorCopperLine).Render(" ── "))
}

func (m *SpecialistWizard) renderArchetypeSelect() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colorIvory).Bold(true).Render("Select Domain Archetype:") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("Pre-loads tailored capabilities, behavioral prompts and domain focus.") + "\n\n")

	for i, arch := range m.archetypes {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorStone)
		descStyle := lipgloss.NewStyle().Foreground(colorAsh)
		if i == m.selArchetype {
			cursor = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("❯ ")
			style = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
			descStyle = lipgloss.NewStyle().Foreground(colorIvory)
		}
		sb.WriteString(cursor + style.Render(arch.Title) + "\n")
		sb.WriteString("    " + descStyle.Render(arch.DomainDesc) + "\n\n")
	}
	return sb.String()
}

func (m *SpecialistWizard) renderNameStep() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colorIvory).Bold(true).Render("Specialist Identifier & Title:") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("Used for @mention delegation and system prompt role identity.") + "\n\n")

	sb.WriteString("  " + lipgloss.NewStyle().Foreground(colorCopperLight).Render("ID / Name: ") + "\n")
	sb.WriteString("  " + m.nameInput.View() + "\n\n")

	sb.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("Tip: Use PascalCase or Snake_Case without spaces (e.g. ") +
		lipgloss.NewStyle().Foreground(colorCopperSpecular).Render("@"+m.nameInput.Value()) +
		lipgloss.NewStyle().Foreground(colorAsh).Render(")") + "\n")

	return sb.String()
}

func (m *SpecialistWizard) renderCapabilitiesStep() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colorIvory).Bold(true).Render("Configure Capabilities & Technical Focus:") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("Press Space to toggle capabilities. Press '+' to add a custom focus.") + "\n\n")

	for i, c := range m.capabilities {
		cursor := "  "
		if i == m.selCapIndex && !m.isAddingCap {
			cursor = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("❯ ")
		}

		check := lipgloss.NewStyle().Foreground(colorAsh).Render("[ ] ")
		if c.Selected {
			check = lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true).Render("[✓] ")
		}

		capNameStyle := lipgloss.NewStyle().Foreground(colorStone)
		if i == m.selCapIndex && !m.isAddingCap {
			capNameStyle = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
		}

		sb.WriteString(cursor + check + capNameStyle.Render(c.Name) + "\n")
	}

	if m.isAddingCap {
		sb.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("+ Add Custom Focus:") + "\n")
		sb.WriteString("  " + m.customCapInput.View() + "\n")
	} else {
		sb.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorCopperLight).Render("[+] Press '+' or 'a' to add a custom capability") + "\n")
	}

	return sb.String()
}

func (m *SpecialistWizard) renderModelStep() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colorIvory).Bold(true).Render("Bind LLM Model (Optional):") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(colorAsh).Render("Choose which local or cloud model powers this sub-agent persona.") + "\n\n")

	// Option 0: Default Engine Model
	cursor0 := "  "
	style0 := lipgloss.NewStyle().Foreground(colorStone)
	if m.selModel == 0 {
		cursor0 = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("❯ ")
		style0 = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
	}
	sb.WriteString(cursor0 + style0.Render("Default Engine Routing") +
		lipgloss.NewStyle().Foreground(colorAsh).Render(" (Inherit current active/fallback model)") + "\n\n")

	for i, mod := range m.models {
		idx := i + 1
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorStone)
		if idx == m.selModel {
			cursor = lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("❯ ")
			style = lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true)
		}
		label := mod.Model
		if mod.Alias != "" {
			label = fmt.Sprintf("%s (%s)", mod.Alias, mod.Model)
		}
		providerBadge := lipgloss.NewStyle().Foreground(colorCopperMid).Render(fmt.Sprintf("[%s]", mod.Provider))
		sb.WriteString(cursor + style.Render(label) + " " + providerBadge + "\n")
	}

	return sb.String()
}

func (m *SpecialistWizard) renderConfirmStep() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colorLedEmerald).Bold(true).Render("✓ Ready to Deploy Specialist Sub-Agent") + "\n\n")

	name := strings.TrimSpace(m.nameInput.Value())
	arch := m.archetypes[m.selArchetype].Title

	boundModel := "Default Engine Model"
	if m.selModel > 0 && m.selModel-1 < len(m.models) {
		cfg := m.models[m.selModel-1]
		if cfg.Alias != "" {
			boundModel = fmt.Sprintf("%s (%s)", cfg.Alias, cfg.Model)
		} else {
			boundModel = cfg.Model
		}
	}

	var activeCaps []string
	for _, c := range m.capabilities {
		if c.Selected {
			activeCaps = append(activeCaps, c.Name)
		}
	}

	sb.WriteString("  " + lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("Identity:   ") + lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("@"+name) + "\n")
	sb.WriteString("  " + lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("Archetype:  ") + lipgloss.NewStyle().Foreground(colorIvory).Render(arch) + "\n")
	sb.WriteString("  " + lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("Bound Model:") + lipgloss.NewStyle().Foreground(colorLedEmerald).Render(boundModel) + "\n")
	sb.WriteString("  " + lipgloss.NewStyle().Foreground(colorCopperLight).Bold(true).Render("Key Focus:  ") + lipgloss.NewStyle().Foreground(colorStone).Render(fmt.Sprintf("%d active capabilities", len(activeCaps))) + "\n\n")

	for _, c := range activeCaps {
		sb.WriteString("    " + lipgloss.NewStyle().Foreground(colorLedEmerald).Render("• ") + lipgloss.NewStyle().Foreground(colorIvory).Render(c) + "\n")
	}

	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(colorCopperSpecular).Bold(true).Render("Press [Enter] to write to Vault and register specialist."))
	return sb.String()
}

