package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	setupTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#d4af37")).Bold(true) // colorGold
	setupFaintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))            // colorMutedGray
	setupDoneStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b"))            // colorEmerald
)

type SetupFinishedMsg struct {
	Result OnboardingResult
}

type OnboardingResult struct {
	Name      string
	Expertise string
	Interests string
	Bio       string
}

type SetupModel struct {
	step     int
	inputs   []textinput.Model
	quitting bool
	Done     bool
	Result   OnboardingResult
	Width    int
}

func NewSetupModel() *SetupModel {
	inputs := make([]textinput.Model, 4)
	
	inputs[0] = textinput.New()
	inputs[0].Placeholder = "What's your name?"
	inputs[0].Focus()
	inputs[0].CharLimit = 32
	inputs[0].Width = 32

	inputs[1] = textinput.New()
	inputs[1].Placeholder = "Level of expertise (e.g. Senior Go Dev, Beginner)"
	inputs[1].CharLimit = 64
	inputs[1].Width = 64

	inputs[2] = textinput.New()
	inputs[2].Placeholder = "Interests (e.g. NATS, Kubernetes, Fitness)"
	inputs[2].CharLimit = 128
	inputs[2].Width = 64

	inputs[3] = textinput.New()
	inputs[3].Placeholder = "Bio (Tell Vraxter a bit about yourself)"
	inputs[3].CharLimit = 256
	inputs[3].Width = 64

	return &SetupModel{
		inputs: inputs,
	}
}

func (m *SetupModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			m.quitting = true
			return m, nil
		case tea.KeyEnter:
			if m.step == len(m.inputs)-1 {
				m.Done = true
				m.Result = OnboardingResult{
					Name:      m.inputs[0].Value(),
					Expertise: m.inputs[1].Value(),
					Interests: m.inputs[2].Value(),
					Bio:       m.inputs[3].Value(),
				}
				return m, func() tea.Msg { return SetupFinishedMsg{Result: m.Result} }
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

func (m *SetupModel) View() string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	if m.quitting {
		return "Setup cancelled."
	}
	if m.Done {
		return "Setup complete! Persisting profile..."
	}

	var s strings.Builder

	headerLeft := setupTitleStyle.Render("◈ USER SETUP")
	headerRight := setupFaintStyle.Render(fmt.Sprintf("%d / %d", m.step+1, len(m.inputs)))

	pad := width - 6 - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if pad < 1 {
		pad = 1
	}
	s.WriteString(headerLeft + strings.Repeat(" ", pad) + headerRight + "\n")
	s.WriteString(setupFaintStyle.Render(strings.Repeat("─", width-6)) + "\n\n")

	steps := []string{"Name", "Expertise", "Interests", "Bio"}

	// Progress indicator
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

	// Current Input
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c8c8c8")).Bold(true)
	s.WriteString(fmt.Sprintf("%s\n", labelStyle.Render(steps[m.step])))
	s.WriteString(m.inputs[m.step].View() + "\n\n")

	s.WriteString(setupFaintStyle.Italic(true).Render("(Press Enter to continue, Esc to cancel)"))

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a2a2a")).
		Padding(1, 2).
		Width(width).
		Render(s.String())
}
