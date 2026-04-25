package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			if m.step == len(m.inputs)-1 {
				m.Done = true
				m.Result = OnboardingResult{
					Name:      m.inputs[0].Value(),
					Expertise: m.inputs[1].Value(),
					Interests: m.inputs[2].Value(),
					Bio:       m.inputs[3].Value(),
				}
				return m, tea.Quit
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
	if m.quitting {
		return "Setup cancelled.\n"
	}
	if m.Done {
		return "Setup complete! Persisting profile...\n"
	}

	var s strings.Builder

	header := titleStyle.Render("Vraxter User Setup")
	s.WriteString(header + "\n\n")

	steps := []string{"Name", "Expertise", "Interests", "Bio"}
	
	// Progress indicator
	for i := 0; i < len(steps); i++ {
		dot := "○ "
		if i == m.step {
			dot = "● "
		} else if i < m.step {
			dot = "✓ "
		}
		
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
		if i == m.step {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00")).Bold(true)
		} else if i < m.step {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF41"))
		}
		
		s.WriteString(style.Render(dot + steps[i]))
		if i < len(steps)-1 {
			s.WriteString("  ")
		}
	}
	s.WriteString("\n\n")

	// Current Input
	s.WriteString(fmt.Sprintf("%s\n", steps[m.step]))
	s.WriteString(m.inputs[m.step].View() + "\n\n")

	s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#777777")).Render("(Press Enter to continue, Ctrl+C to cancel)"))

	return appStyle.Render(s.String())
}
