package app

import tea "charm.land/bubbletea/v2"

// This file is a stand-in for the onboarding branch and nothing else lives in
// it: when that branch merges its internal/app/onboarding.go, delete this file
// whole. It exists so that the menu can already carry the entry that replays
// the introduction, wired to the two names the real implementation provides.

// NewOnboarding builds the introduction. The stand-in is a screen that says
// what it is and leaves on any key, so the entry that opens it can be walked
// end to end before the real one lands.
func NewOnboarding(d Deps) (Screen, error) {
	return &onboardingStub{deps: d}, nil
}

// OnboardingSeen reports whether this machine has been through the
// introduction. The stand-in answers yes so that no first-run path opens the
// stand-in screen uninvited: first-run behaviour belongs to the real
// implementation, which also records the flag.
func OnboardingSeen(Deps) bool { return true }

type onboardingStub struct {
	deps          Deps
	width, height int
}

func (s *onboardingStub) Init() tea.Cmd { return nil }

func (s *onboardingStub) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return s, Back()
	}
	return s, nil
}

func (s *onboardingStub) View() tea.View {
	st := shellStyles(s.deps)
	content := []string{paint(st, &st.PanelText, "The introduction is not part of this build.")}
	status := paint(st, &st.Status, hintLine(s.width, "any key back"))
	return tea.NewView(textFrame(st, s.width, s.height, content, status))
}
