// Package tui is the interactive front end: pick a provider, pick an event,
// send it.
//
// It is a browser rather than a walkthrough, which is the opposite of the
// decision [flint] made for flashing firmware. Sending a webhook is not a
// procedure with an order; it is a thing you do twenty times in an afternoon
// while an application runs in the next window, changing one variable each
// time. So the whole state is on screen at once and every control is one key.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JustSteveKing/herald/deliver"
	"github.com/JustSteveKing/herald/internal/state"
	"github.com/JustSteveKing/herald/pack"
)

type pane int

const (
	providerPane pane = iota
	eventPane
)

type field int

const (
	noField field = iota
	targetField
	secretField
)

// Model is the whole interface.
type Model struct {
	providers []pack.Provider
	fixtures  []pack.Fixture

	provider int
	event    int
	focus    pane
	editing  field

	input  textinput.Model
	secret string
	state  *state.State

	log     []entry
	err     error
	width   int
	height  int
	quitted bool
}

type entry struct {
	when     time.Time
	provider string
	event    string
	note     string
	status   int
	err      error
	duration time.Duration
}

type sent struct {
	provider string
	event    string
	note     string
	results  []deliver.Result
	err      error
}

// New builds the model over the loaded packs.
func New(set *pack.Set) Model {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 2048

	m := Model{
		providers: set.All(),
		state:     state.Load(),
		input:     input,
	}

	for i, p := range m.providers {
		if p.ID == m.state.Provider {
			m.provider = i
			break
		}
	}

	m.loadFixtures()
	return m
}

// Run starts the program.
func Run(set *pack.Set) error {
	program := tea.NewProgram(New(set), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (m *Model) loadFixtures() {
	m.fixtures = nil
	m.event = 0

	if len(m.providers) == 0 {
		return
	}

	fixtures, err := m.current().Fixtures()
	if err != nil {
		m.err = err
		return
	}

	m.fixtures = fixtures
}

func (m Model) current() pack.Provider { return m.providers[m.provider] }

func (m Model) target() string {
	return m.state.Target(m.current().ID)
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles one message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case sent:
		return m.record(msg), nil

	case tea.KeyMsg:
		if m.editing != noField {
			return m.updateEditing(msg)
		}
		return m.updateBrowsing(msg)
	}

	return m, nil
}

func (m Model) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		if m.editing == targetField {
			m.state.SetTarget(m.current().ID, value)
			m.state.Save()
		} else {
			m.secret = value
		}
		m.editing = noField
		m.input.Blur()
		return m, nil

	case "esc":
		m.editing = noField
		m.input.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateBrowsing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.state.Provider = m.current().ID
		m.state.Save()
		m.quitted = true
		return m, tea.Quit

	case "tab", "left", "right", "h", "l":
		if m.focus == providerPane {
			m.focus = eventPane
		} else {
			m.focus = providerPane
		}
		return m, nil

	case "up", "k":
		return m.move(-1), nil

	case "down", "j":
		return m.move(1), nil

	case "t":
		m.editing = targetField
		m.input.SetValue(m.target())
		m.input.CursorEnd()
		m.input.Focus()
		return m, textinput.Blink

	case "s":
		m.editing = secretField
		m.input.SetValue(m.secret)
		m.input.CursorEnd()
		m.input.Focus()
		return m, textinput.Blink

	case "enter":
		return m, m.send("", deliver.Options{})

	case "d":
		// A provider retry: the same delivery, twice. The one a handler that
		// is not idempotent fails.
		return m, m.send("duplicate", deliver.Options{Count: 2, Duplicate: true})

	case "b":
		return m, m.send("bad signature", deliver.Options{Corrupt: true})

	case "r":
		// Old enough to be outside every tolerance any provider here documents.
		return m, m.send("replay, 6m old", deliver.Options{Skew: -6 * time.Minute})

	case "x":
		return m, m.send("burst of 10", deliver.Options{Count: 10})
	}

	return m, nil
}

func (m Model) move(delta int) Model {
	if m.focus == providerPane {
		m.provider = clamp(m.provider+delta, len(m.providers))
		m.loadFixtures()
		return m
	}

	m.event = clamp(m.event+delta, len(m.fixtures))
	return m
}

func clamp(i, length int) int {
	if length == 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= length {
		return length - 1
	}
	return i
}

func (m Model) send(note string, opts deliver.Options) tea.Cmd {
	if len(m.fixtures) == 0 {
		return nil
	}

	provider := m.current()
	fixture := m.fixtures[m.event]
	target := m.target()
	secret := m.secret
	if secret == "" {
		secret = provider.Secret.Example
	}

	if target == "" {
		return func() tea.Msg {
			return sent{
				provider: provider.ID,
				event:    fixture.ID,
				err:      fmt.Errorf("no target: press t and give it a URL"),
			}
		}
	}

	opts.Target = target
	opts.Secret = secret

	return func() tea.Msg {
		full, err := provider.Fixture(fixture.ID)
		if err != nil {
			return sent{provider: provider.ID, event: fixture.ID, note: note, err: err}
		}

		results, err := deliver.Send(context.Background(), provider, full, opts)
		return sent{provider: provider.ID, event: fixture.ID, note: note, results: results, err: err}
	}
}

func (m Model) record(msg sent) Model {
	if msg.err != nil {
		m.log = append(m.log, entry{when: time.Now(), provider: msg.provider, event: msg.event, note: msg.note, err: msg.err})
		return m
	}

	for _, r := range msg.results {
		m.log = append(m.log, entry{
			when:     time.Now(),
			provider: msg.provider,
			event:    msg.event,
			note:     msg.note,
			status:   r.Status,
			err:      r.Err,
			duration: r.Duration,
		})
	}

	// Keep the log short. Anything older is history the terminal already has
	// if it mattered.
	if len(m.log) > 200 {
		m.log = m.log[len(m.log)-200:]
	}

	return m
}

func (m Model) View() string {
	if m.quitted {
		return ""
	}
	if len(m.providers) == 0 {
		return "No providers. Try herald sync.\n"
	}

	width := m.width
	if width < 60 {
		width = 96
	}

	left := width / 3
	right := width - left - 7

	var b strings.Builder
	b.WriteString(titleStyle.Render("herald"))
	b.WriteString(mutedStyle.Render("  send a webhook that verifies"))
	b.WriteString("\n\n")

	providers, events := m.providerRows(), m.eventRows()

	// Both panes get the same height so their borders line up. Letting each
	// size itself makes the shorter one look broken every time the provider
	// list and the event list disagree, which is always.
	height := max(len(providers), len(events)) + 1

	b.WriteString(lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().MarginRight(1).Render(m.renderPane("providers", providers, left, height, m.focus == providerPane)),
		m.renderPane("events", events, right, height, m.focus == eventPane),
	))
	b.WriteString("\n")

	b.WriteString(m.renderFields())
	b.WriteString("\n")
	b.WriteString(m.renderNotes())
	b.WriteString(m.renderLog())
	b.WriteString("\n")
	b.WriteString(m.renderHelp())

	return b.String()
}

func (m Model) renderPane(title string, rows []string, width, height int, focused bool) string {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}

	body := strings.Join(rows, "\n")
	if body == "" {
		body = mutedStyle.Render("nothing here")
	}

	return style.Width(width).Height(height).Render(titleStyle.Render(title) + "\n" + body)
}

func (m Model) providerRows() []string {
	rows := make([]string, 0, len(m.providers))

	for i, p := range m.providers {
		label := p.ID
		if len(p.Signing) == 0 {
			label += mutedStyle.Render(" unsigned")
		}
		rows = append(rows, row(label, i == m.provider, m.focus == providerPane))
	}

	return rows
}

func (m Model) eventRows() []string {
	rows := make([]string, 0, len(m.fixtures))

	for i, f := range m.fixtures {
		rows = append(rows, row(f.ID, i == m.event, m.focus == eventPane))
	}

	return rows
}

func row(label string, selected, focused bool) string {
	if !selected {
		return "  " + label
	}
	if focused {
		return selectedStyle.Render("> " + label)
	}
	return mutedStyle.Render("> " + label)
}

func (m Model) renderFields() string {
	var b strings.Builder

	target := m.target()
	if target == "" {
		target = mutedStyle.Render("not set, press t")
	}

	secret := m.secret
	source := ""
	if secret == "" {
		secret, source = m.current().Secret.Example, mutedStyle.Render("  the pack's example")
	}

	if m.editing == targetField {
		b.WriteString(fmt.Sprintf("target  %s\n", m.input.View()))
	} else {
		b.WriteString(fmt.Sprintf("target  %s\n", target))
	}

	if m.editing == secretField {
		b.WriteString(fmt.Sprintf("secret  %s\n", m.input.View()))
	} else {
		b.WriteString(fmt.Sprintf("secret  %s%s\n", secret, source))
	}

	return b.String()
}

func (m Model) renderNotes() string {
	p := m.current()
	if p.Notes == "" {
		return ""
	}

	// Only the first line. The full caveat is in `herald events <provider>`,
	// and a wall of text above the log would be scrolled past rather than read.
	first := strings.SplitN(strings.TrimSpace(p.Notes), "\n", 2)[0]

	return warnStyle.Render("! "+first) + "\n"
}

func (m Model) renderLog() string {
	if len(m.log) == 0 {
		return "\n" + mutedStyle.Render("Nothing sent yet.") + "\n"
	}

	show := m.log
	if len(show) > 8 {
		show = show[len(show)-8:]
	}

	var b strings.Builder
	b.WriteString("\n")

	for _, e := range show {
		mark := goodStyle.Render("ok ")
		detail := fmt.Sprintf("%d  %s", e.status, e.duration.Round(time.Millisecond))

		switch {
		case e.err != nil:
			mark, detail = badStyle.Render("err"), e.err.Error()
		case e.status < 200 || e.status >= 300:
			mark = badStyle.Render("bad")
		}

		note := ""
		if e.note != "" {
			note = mutedStyle.Render("  " + e.note)
		}

		b.WriteString(fmt.Sprintf("%s %s  %s%s  %s\n",
			mark, e.when.Format("15:04:05"), e.event, note, detail))
	}

	return b.String()
}

func (m Model) renderHelp() string {
	if m.editing != noField {
		return mutedStyle.Render("enter save   esc cancel")
	}

	return mutedStyle.Render(
		"enter send   d duplicate   b bad sig   r replay 6m   x burst 10   t target   s secret   tab pane   q quit")
}
