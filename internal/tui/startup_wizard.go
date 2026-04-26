package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patagonicrune/vraxter/internal/services"
)

type StartupWizardState int

const (
	StateSplash StartupWizardState = iota
	StateUserProfile
	StateProviderConfig
	StateModelRegister
	StateFinished
)

type StartupWizard struct {
	state       StartupWizardState
	width       int
	height      int
	
	// Sub-Wizards
	userWizard   *UserWizardModel
	provWizard   *ProviderWizard
	modelWizard  *ModelWizard
	
	// Dependencies for child creation
	provMgr      *services.ProviderManager
	modelMgr     *services.ModelManager
	
	quitting     bool
	Done         bool
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
	case tea.KeyMsg:
		if m.state == StateSplash {
			if msg.Type == tea.KeyEnter {
				m.state = StateUserProfile
				return m, m.userWizard.Init()
			}
			if msg.Type == tea.KeyEsc {
				m.quitting = true
				m.Done = true
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	switch m.state {
	case StateUserProfile:
		var sub tea.Model
		sub, cmd = m.userWizard.Update(msg)
		m.userWizard = sub.(*UserWizardModel)
		if m.userWizard.Done {
			m.state = StateProviderConfig
			return m, m.provWizard.Init()
		}
	case StateProviderConfig:
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
		if km, ok := msg.(tea.KeyMsg); ok && km.Type == tea.KeyEnter {
			m.Done = true
			return m, nil
		}
	}

	return m, cmd
}

func (m *StartupWizard) View() string {
	if m.quitting {
		return "Onboarding cancelled. Vraxter is ready when you are."
	}

	switch m.state {
	case StateSplash:
		return m.renderSplash()
	case StateUserProfile:
		return m.userWizard.View()
	case StateProviderConfig:
		return m.provWizard.View()
	case StateModelRegister:
		return m.modelWizard.View()
	case StateFinished:
		return m.renderFinished()
	}
	return ""
}

func (m *StartupWizard) renderSplash() string {
	width := m.width
	if width <= 0 { width = 80 }

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorGold).Bold(true).Render("    ◈ WELCOME TO VRAXTER") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).Render("    The autonomous workstation for the modern engineer.") + "\n\n")
	
	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Width(width - 8).Render(
		"This wizard will guide you through the initial configuration:\n\n" +
		" 1. Create your Identity\n" +
		" 2. Connect a Provider (OpenAI, Google, etc.)\n" +
		" 3. Register your first Model\n",
	))
	
	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorGold).Render("Press ENTER to begin your journey..."))

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a2a2a")).
		Padding(1, 2).
		Width(width).
		Render(s.String())
}

func (m *StartupWizard) renderFinished() string {
	width := m.width
	if width <= 0 { width = 80 }

	var s strings.Builder
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(setupDoneStyle.GetForeground()).Bold(true).Render("    ◈ CONFIGURATION COMPLETE") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).Render("    Vraxter has been initialized and is ready for action.") + "\n\n")
	
	s.WriteString(lipgloss.NewStyle().PaddingLeft(4).Width(width - 8).Render(
		"You can now start chatting with your models. Simply type your request \n" +
		"in the console after exiting this wizard.\n\n" +
		"To modify these settings later, use:\n" +
		" /user setup      /providers setup      /models setup",
	))
	
	s.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(4).Foreground(colorGold).Render("Press ENTER to enter the workstation..."))

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2a2a2a")).
		Padding(1, 2).
		Width(width).
		Render(s.String())
}
