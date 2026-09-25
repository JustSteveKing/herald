package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JustSteveKing/herald/deliver"
	"github.com/JustSteveKing/herald/pack"
)

func model(t *testing.T) Model {
	t.Helper()

	// Keep the test off the real config directory, so running the suite never
	// touches state a person is using.
	t.Setenv("HERALD_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))

	set, err := pack.Default()
	if err != nil {
		t.Fatalf("loading packs: %v", err)
	}

	m := New(set)
	m.width, m.height = 100, 40

	return m
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()

	for _, key := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		switch key {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		}

		next, _ := m.Update(msg)
		m = next.(Model)
	}

	return m
}

func TestOpensOnAProviderWithItsEvents(t *testing.T) {
	m := model(t)

	if len(m.providers) == 0 {
		t.Fatal("no providers")
	}
	if len(m.fixtures) == 0 {
		t.Fatalf("%s has no fixtures loaded", m.current().ID)
	}

	view := m.View()
	if !strings.Contains(view, m.current().ID) {
		t.Errorf("the selected provider is not in the view:\n%s", view)
	}
	if !strings.Contains(view, m.fixtures[0].ID) {
		t.Error("the first event is not in the view")
	}
}

func TestMovingProviderReloadsEvents(t *testing.T) {
	m := model(t)
	first := m.current().ID
	firstEvents := len(m.fixtures)

	m = press(t, m, "down")

	if m.current().ID == first {
		t.Fatal("down did not move")
	}
	// The pane has to follow, or you send the previous provider's event under
	// the new provider's signature.
	if len(m.fixtures) == 0 {
		t.Error("events did not reload for the new provider")
	}
	if m.event != 0 {
		t.Errorf("event index stayed at %d after changing provider", m.event)
	}

	_ = firstEvents
}

func TestTabMovesFocusAndEventSelectionFollows(t *testing.T) {
	m := model(t)
	m = press(t, m, "tab")

	if m.focus != eventPane {
		t.Fatal("tab did not move focus to the events pane")
	}

	before := m.provider
	m = press(t, m, "down")

	if m.provider != before {
		t.Error("moving in the events pane changed the provider")
	}
	if m.event != 1 {
		t.Errorf("event index is %d, want 1", m.event)
	}
}

func TestSendingWithoutATargetSaysSo(t *testing.T) {
	m := model(t)

	cmd := m.send("", deliver.Options{})
	if cmd == nil {
		t.Fatal("no command returned")
	}

	msg, ok := cmd().(sent)
	if !ok {
		t.Fatalf("unexpected message %T", cmd())
	}
	if msg.err == nil {
		t.Fatal("sending with no target did not error")
	}
	if !strings.Contains(msg.err.Error(), "press t") {
		t.Errorf("the error does not say how to fix it: %v", msg.err)
	}
}

func TestTargetIsRememberedPerProvider(t *testing.T) {
	m := model(t)
	first := m.current().ID

	m = press(t, m, "t")
	if m.editing != targetField {
		t.Fatal("t did not start editing the target")
	}

	m.input.SetValue("http://localhost:8000/one")
	m = press(t, m, "enter")

	if got := m.target(); got != "http://localhost:8000/one" {
		t.Fatalf("target is %q", got)
	}

	m = press(t, m, "down")
	if m.target() != "" {
		t.Error("the next provider inherited the previous target")
	}

	m = press(t, m, "up")
	if got := m.target(); got != "http://localhost:8000/one" {
		t.Errorf("coming back to %s lost its target: %q", first, got)
	}
}

func TestSecretIsNotWrittenToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("HERALD_STATE_FILE", path)

	set, err := pack.Default()
	if err != nil {
		t.Fatal(err)
	}

	m := New(set)
	m = press(t, m, "s")
	m.input.SetValue("whsec_a_real_secret_please_do_not_save_me")
	m = press(t, m, "enter")
	m = press(t, m, "q")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no state file written: %v", err)
	}

	if strings.Contains(string(raw), "whsec_a_real_secret") {
		t.Error("the secret was persisted, and it must not be")
	}
}

func TestEscapeAbandonsAnEdit(t *testing.T) {
	m := model(t)

	m = press(t, m, "t")
	m.input.SetValue("http://localhost:8000/typo")
	m = press(t, m, "esc")

	if m.editing != noField {
		t.Error("esc did not leave the field")
	}
	if m.target() != "" {
		t.Errorf("esc saved the value anyway: %q", m.target())
	}
}
