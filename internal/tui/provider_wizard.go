package tui

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patagonicrune/vraxter/internal/services"
)

type ProviderWizard struct {
	manager *services.ProviderManager
	step    int
	name    string
	pType   string
	apiKey  string
	baseURL string
	err     error
	width   int
	done    bool
}

func NewProviderWizard(mgr *services.ProviderManager) *ProviderWizard {
	return &ProviderWizard{manager: mgr, step: 0}
}

func (m *ProviderWizard) Init() tea.Cmd {
	return nil
}

func (m *ProviderWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.done = true
			return m, nil
		case "enter":
			m.step++
			if m.step > 3 || (m.pType == "ollama" && m.step == 2) {
				// Finalize
				id, err := m.manager.AddProvider(context.Background(), m.name, m.pType, m.apiKey, m.baseURL)
				if err != nil {
					m.err = err
					m.step-- // Go back to fix
					return m, nil
				}
				fmt.Printf("\n✅ Provider added with ID: %s\n", id)
				m.done = true
			}
		case "backspace":
			// Simple backspace for current field
			switch m.step {
			case 0: if len(m.name) > 0 { m.name = m.name[:len(m.name)-1] }
			case 1: if len(m.pType) > 0 { m.pType = m.pType[:len(m.pType)-1] }
			case 2: if len(m.apiKey) > 0 { m.apiKey = m.apiKey[:len(m.apiKey)-1] }
			case 3: if len(m.baseURL) > 0 { m.baseURL = m.baseURL[:len(m.baseURL)-1] }
			}
		default:
			if len(msg.String()) == 1 {
				switch m.step {
				case 0: m.name += msg.String()
				case 1: m.pType += msg.String()
				case 2: m.apiKey += msg.String()
				case 3: m.baseURL += msg.String()
				}
			}
		}
	}
	return m, nil
}

func (m *ProviderWizard) View() string {
	if m.done {
		return ""
	}
	
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("196")).
		Render("◈ PROVIDER SETUP WIZARD")

	var body string
	switch m.step {
	case 0:
		body = fmt.Sprintf("Step 1: Enter Provider Name (e.g., Google Personal)\n> %s", m.name)
	case 1:
		body = fmt.Sprintf("Step 2: Enter Type (google, openai, anthropic, ollama)\n> %s", m.pType)
	case 2:
		body = fmt.Sprintf("Step 3: Enter API Key\n> %s", m.apiKey)
	case 3:
		body = fmt.Sprintf("Step 4: Enter Base URL (Optional)\n> %s", m.baseURL)
	}

	if m.err != nil {
		body += lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("\n\n❌ Error: %v", m.err))
	}

	footer := "\n\n(Enter: Next | Esc: Cancel)"
	
	return lipgloss.Place(m.width, 10, lipgloss.Center, lipgloss.Center, 
		lipgloss.JoinVertical(lipgloss.Center, header, body, footer))
}
