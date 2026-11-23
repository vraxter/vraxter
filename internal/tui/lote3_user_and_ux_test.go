// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/vraxter/vraxter/internal/db"
)

func TestTUI_SetupFinishedMsg_CreatesDefaultUserWhenMissing(t *testing.T) {
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	m := &Model{
		width:    100,
		height:   36,
		appStore: store,
		UserName: "YOU",
	}

	// Fresh DB has no users (GetDefaultUser returns nil, nil) — must NOT panic and must persist new user.
	updated, _ := m.Update(SetupFinishedMsg{
		Result: OnboardingResult{
			Name:      "Diego",
			Expertise: "Systems Architect",
			Interests: "WASM, DDIL",
			Bio:       "Sovereign AI",
		},
	})
	m = updated.(*Model)

	if m.UserName != "Diego" {
		t.Fatalf("expected UserName='Diego', got %q", m.UserName)
	}

	repo := db.NewUserRepository(store)
	user, err := repo.GetDefaultUser(context.Background())
	if err != nil || user == nil {
		t.Fatalf("expected created user in DB, got user=%v err=%v", user, err)
	}
	if user.Name != "Diego" || user.Expertise != "Systems Architect" {
		t.Fatalf("unexpected persisted user: %+v", user)
	}

	// Second SetupFinishedMsg updates existing user cleanly
	updated, _ = m.Update(SetupFinishedMsg{
		Result: OnboardingResult{
			Name:      "Diego Updated",
			Expertise: "Principal Architect",
			Interests: "Security",
			Bio:       "Updated bio",
		},
	})
	m = updated.(*Model)

	user2, err := repo.GetDefaultUser(context.Background())
	if err != nil || user2 == nil || user2.Name != "Diego Updated" {
		t.Fatalf("expected updated user 'Diego Updated', got user=%+v err=%v", user2, err)
	}
}

func TestTUI_QuitCommandReturnsTeaQuit(t *testing.T) {
	for _, cmdStr := range []string{"/quit", "/exit"} {
		m := &Model{width: 100, height: 36}
		cmd := m.handleSlashCommand(cmdStr)
		if cmd == nil {
			t.Fatalf("%s must return non-nil tea.Quit cmd", cmdStr)
		}
		if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
			t.Fatalf("%s returned %T, want tea.QuitMsg", cmdStr, cmd())
		}
	}
}

func TestTUI_ShowHelpOverlayRendersAndDismissesWithEsc(t *testing.T) {
	s := spinner.New()
	s.Spinner = sigilSpinner
	ta := textarea.New()
	ta.SetWidth(80)
	ta.SetHeight(1)
	vp := viewport.New()
	vp.SetWidth(100)
	vp.SetHeight(20)
	r, err := buildRenderer(104)
	if err != nil {
		t.Fatalf("buildRenderer: %v", err)
	}

	m := &Model{
		width:        104,
		height:       36,
		Ready:        true,
		Input:        ta,
		Viewport:     vp,
		renderer:     r,
		Spinner:      s,
		SessionID:    "help-test-session",
		CurrentFocus: "vraxter",
		ShowHelp:     true,
	}

	v := m.View()
	assertMaxSGRParams(t, "View(ShowHelp)", v.Content)
	plain := stripANSI(v.Content)
	if !strings.Contains(plain, "COMMAND & SHORTCUT REFERENCE") {
		t.Fatalf("expected ShowHelp overlay in View, got:\n%s", plain)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(*Model)
	if m.ShowHelp {
		t.Fatal("expected ESC to close ShowHelp overlay")
	}
}
