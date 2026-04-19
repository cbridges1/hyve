package shared

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ErrBack is the sentinel returned when the user selects "← Back" in any menu.
var ErrBack = errors.New("back")

// HyveTheme returns a yellow-and-blue huh theme for all interactive forms.
func HyveTheme() *huh.Theme {
	t := huh.ThemeBase()

	var (
		yellow   = lipgloss.Color("#F5C518")
		blue     = lipgloss.Color("#4A9FD5")
		blueDark = lipgloss.Color("#1E6FA8")
		muted    = lipgloss.Color("#6B7280")
		white    = lipgloss.Color("#F9FAFB")
		red      = lipgloss.Color("#EF4444")
	)

	// Focused field styles
	t.Focused.Base = t.Focused.Base.BorderForeground(blue)
	t.Focused.Card = t.Focused.Base

	t.Focused.Title = t.Focused.Title.Foreground(yellow).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(yellow).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(muted)

	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(red)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(red)

	// Select / multi-select
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(yellow)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(yellow)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(yellow)
	t.Focused.Option = t.Focused.Option.Foreground(white)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(yellow)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(blue)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(blue).SetString("[✓] ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(white)
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(muted).SetString("[ ] ")

	// Buttons
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(yellow).Background(blueDark).Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(muted).Background(lipgloss.Color("#1F2937"))

	// Text input
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(yellow)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(muted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(blue)
	t.Focused.TextInput.Text = t.Focused.TextInput.Text.Foreground(white)

	// Blurred inherits focused then overrides border
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}

// RequireNotEmpty is a huh validation function that rejects blank input.
// Use with huh.NewInput().Validate(shared.RequireNotEmpty).
func RequireNotEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("this field is required")
	}
	return nil
}

// NewForm wraps huh.NewForm and applies the Hyve theme automatically.
func NewForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(HyveTheme())
}

// OptionGroup holds a named group of select options for two-level menus.
type OptionGroup struct {
	Name    string
	Options []huh.Option[string]
}

// SelectFromGroups shows a two-level select: first the group name, then the
// items inside that group.
func SelectFromGroups(title string, groups []OptionGroup, placeholder string, value *string) error {
	const manualKey = "__manual__"
	const backKey = "__back__"
	for {
		groupOpts := make([]huh.Option[string], 0, len(groups)+2)
		groupOpts = append(groupOpts, huh.NewOption("Enter manually...", manualKey))
		for _, g := range groups {
			groupOpts = append(groupOpts, huh.NewOption(g.Name, g.Name))
		}
		groupOpts = append(groupOpts, huh.NewOption("← Back", backKey))

		selectedGroup := ""
		if err := NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title + " — select category").
					Options(groupOpts...).
					Value(&selectedGroup),
			),
		).Run(); err != nil {
			return err
		}
		if selectedGroup == backKey {
			return ErrBack
		}
		if selectedGroup == manualKey {
			return NewForm(
				huh.NewGroup(
					huh.NewInput().Title(title).Placeholder(placeholder).Validate(RequireNotEmpty).Value(value),
				),
			).Run()
		}

		// Find the chosen group's items
		var items []huh.Option[string]
		for _, g := range groups {
			if g.Name == selectedGroup {
				items = g.Options
				break
			}
		}
		itemOpts := make([]huh.Option[string], 0, len(items)+1)
		itemOpts = append(itemOpts, items...)
		itemOpts = append(itemOpts, huh.NewOption("← Back to categories", backKey))

		selection := ""
		if err := NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title + " — " + selectedGroup).
					Options(itemOpts...).
					Value(&selection),
			),
		).Run(); err != nil {
			return err
		}
		if selection == backKey {
			continue // re-show group list
		}
		*value = selection
		return nil
	}
}

// SelectFromGroupsOptional is like SelectFromGroups but prepends a
// "No change (keep current)" option at the group level.
func SelectFromGroupsOptional(title string, groups []OptionGroup, value *string) error {
	const manualKey = "__manual__"
	const backKey = "__back__"
	const noChangeKey = "__nochange__"
	for {
		groupOpts := make([]huh.Option[string], 0, len(groups)+3)
		groupOpts = append(groupOpts, huh.NewOption("Enter manually...", manualKey))
		groupOpts = append(groupOpts, huh.NewOption("No change (keep current)", noChangeKey))
		for _, g := range groups {
			groupOpts = append(groupOpts, huh.NewOption(g.Name, g.Name))
		}
		groupOpts = append(groupOpts, huh.NewOption("← Back", backKey))

		selectedGroup := ""
		if err := NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title + " — select category").
					Options(groupOpts...).
					Value(&selectedGroup),
			),
		).Run(); err != nil {
			return err
		}
		if selectedGroup == backKey {
			return ErrBack
		}
		if selectedGroup == noChangeKey {
			*value = ""
			return nil
		}
		if selectedGroup == manualKey {
			return NewForm(
				huh.NewGroup(
					huh.NewInput().Title(title + " (leave blank to keep current)").Value(value),
				),
			).Run()
		}

		var items []huh.Option[string]
		for _, g := range groups {
			if g.Name == selectedGroup {
				items = g.Options
				break
			}
		}
		itemOpts := make([]huh.Option[string], 0, len(items)+1)
		itemOpts = append(itemOpts, items...)
		itemOpts = append(itemOpts, huh.NewOption("← Back to categories", backKey))

		selection := ""
		if err := NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title + " — " + selectedGroup).
					Options(itemOpts...).
					Value(&selection),
			),
		).Run(); err != nil {
			return err
		}
		if selection == backKey {
			continue
		}
		*value = selection
		return nil
	}
}

// ── Expiry date picker (Bubble Tea) ──────────────────────────────────────────

var expiryMonthNames = [12]string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

const (
	expiryFieldYear = iota
	expiryFieldMonth
	expiryFieldDay
	expiryFieldHour
	expiryFieldMinute
	expiryFieldCount
)

type expiryPickerModel struct {
	focused   int
	year      int
	month     int // 1–12
	day       int // 1–31
	hour      int // 0–23
	minute    int // 0–59
	done      bool
	noExpiry  bool // esc — skip expiry, continue
	cancelled bool // ctrl+c — abort the whole operation
}

func newExpiryPickerModel(current string) expiryPickerModel {
	now := time.Now()
	m := expiryPickerModel{
		year:   now.Year(),
		month:  int(now.Month()),
		day:    now.Day(),
		hour:   now.Hour(),
		minute: now.Minute(),
	}
	if current != "" {
		if t, err := time.Parse(time.RFC3339, current); err == nil {
			m.year = t.Year()
			m.month = int(t.Month())
			m.day = t.Day()
			m.hour = t.Hour()
			m.minute = t.Minute()
		}
	}
	return m
}

func (m expiryPickerModel) Init() tea.Cmd { return nil }

func (m expiryPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c":
		m.cancelled = true
		m.done = true
		return m, tea.Quit
	case "esc":
		m.noExpiry = true
		m.done = true
		return m, tea.Quit
	case "enter":
		m.done = true
		return m, tea.Quit
	case "left", "h":
		if m.focused > 0 {
			m.focused--
		}
	case "right", "l":
		if m.focused < expiryFieldCount-1 {
			m.focused++
		}
	case "up", "k":
		m = m.step(1)
	case "down", "j":
		m = m.step(-1)
	}
	return m, nil
}

func (m expiryPickerModel) step(delta int) expiryPickerModel {
	switch m.focused {
	case expiryFieldYear:
		m.year += delta
	case expiryFieldMonth:
		m.month = ((m.month - 1 + delta + 12) % 12) + 1
		if d := expiryDaysInMonth(m.year, m.month); m.day > d {
			m.day = d
		}
	case expiryFieldDay:
		n := expiryDaysInMonth(m.year, m.month)
		m.day = ((m.day - 1 + delta + n) % n) + 1
	case expiryFieldHour:
		m.hour = (m.hour + delta + 24) % 24
	case expiryFieldMinute:
		m.minute = (m.minute + delta + 60) % 60
	}
	return m
}

func expiryDaysInMonth(year, month int) int {
	return time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Day()
}

func (m expiryPickerModel) View() string {
	yellow := lipgloss.Color("#F5C518")
	blue := lipgloss.Color("#4A9FD5")
	muted := lipgloss.Color("#6B7280")
	white := lipgloss.Color("#F9FAFB")

	focusedVal := lipgloss.NewStyle().Foreground(yellow).Bold(true)
	normalVal := lipgloss.NewStyle().Foreground(white)
	focusedLbl := lipgloss.NewStyle().Foreground(blue)
	normalLbl := lipgloss.NewStyle().Foreground(muted)
	titleStyle := lipgloss.NewStyle().Foreground(yellow).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(muted)
	arrowStyle := lipgloss.NewStyle().Foreground(yellow)
	dimArrow := lipgloss.NewStyle().Foreground(lipgloss.Color("#374151"))

	type fieldDef struct{ value, label string }
	fields := [expiryFieldCount]fieldDef{
		{fmt.Sprintf("%d", m.year), "Year"},
		{expiryMonthNames[m.month-1], "Month"},
		{fmt.Sprintf("%02d", m.day), "Day"},
		{fmt.Sprintf("%02d", m.hour), "Hour"},
		{fmt.Sprintf("%02d", m.minute), "Min"},
	}

	var upRow, valRow, dnRow, lblRow strings.Builder
	for i, f := range fields {
		if i > 0 {
			sep := "   "
			upRow.WriteString(sep)
			valRow.WriteString(sep)
			dnRow.WriteString(sep)
			lblRow.WriteString(sep)
		}
		w := max(len(f.value), len(f.label)) + 2
		if i == m.focused {
			upRow.WriteString(arrowStyle.Render(padCenter("▲", w)))
			valRow.WriteString(focusedVal.Render(padCenter(f.value, w)))
			dnRow.WriteString(arrowStyle.Render(padCenter("▼", w)))
			lblRow.WriteString(focusedLbl.Render(padCenter(f.label, w)))
		} else {
			upRow.WriteString(dimArrow.Render(padCenter(" ", w)))
			valRow.WriteString(normalVal.Render(padCenter(f.value, w)))
			dnRow.WriteString(dimArrow.Render(padCenter(" ", w)))
			lblRow.WriteString(normalLbl.Render(padCenter(f.label, w)))
		}
	}

	hint := hintStyle.Render("↑ ↓  change   ← →  navigate   enter  set expiry   esc  no expiry   ctrl+c  cancel")

	return "\n" +
		"  " + titleStyle.Render("Expiry date") + "\n\n" +
		"  " + upRow.String() + "\n" +
		"  " + valRow.String() + "\n" +
		"  " + dnRow.String() + "\n" +
		"  " + lblRow.String() + "\n\n" +
		"  " + hint + "\n\n"
}

func padCenter(s string, width int) string {
	if len(s) >= width {
		return s
	}
	total := width - len(s)
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// PromptExpiresAt runs a single-screen date picker with ←/→ navigation between
// Year, Month, Day, Hour, and Minute fields. Defaults to the current date/time.
//
//   - enter    → returns the RFC 3339 timestamp
//   - esc      → returns ("", nil)  — no expiry, execution continues
//   - ctrl+c   → returns ("", ErrBack) — cancels the entire operation
func PromptExpiresAt(current string) (string, error) {
	m, err := tea.NewProgram(newExpiryPickerModel(current)).Run()
	if err != nil {
		return "", err
	}
	result := m.(expiryPickerModel)
	if result.cancelled {
		return "", ErrBack
	}
	if result.noExpiry {
		return "", nil
	}
	raw := fmt.Sprintf("%d-%02d-%02dT%02d:%02d:00Z",
		result.year, result.month, result.day, result.hour, result.minute)
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", fmt.Errorf("invalid date: %w", err)
	}
	return t.Format(time.RFC3339), nil
}

// ── Cron schedule picker ─────────────────────────────────────────────────────

// CronNextOccurrence returns the next time after `from` that matches the given
// 5-field cron expression (minute hour dom month dow). Supports *, single
// numbers, ranges (1-5), and comma-separated lists (1,2,3).
func CronNextOccurrence(expr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	matchField := func(field string, val int) (bool, error) {
		if field == "*" {
			return true, nil
		}
		for _, part := range strings.Split(field, ",") {
			if strings.Contains(part, "-") {
				bounds := strings.SplitN(part, "-", 2)
				lo, err1 := strconv.Atoi(bounds[0])
				hi, err2 := strconv.Atoi(bounds[1])
				if err1 != nil || err2 != nil {
					return false, fmt.Errorf("invalid range %q", part)
				}
				if val >= lo && val <= hi {
					return true, nil
				}
			} else {
				n, err := strconv.Atoi(part)
				if err != nil {
					return false, fmt.Errorf("invalid field value %q", part)
				}
				if val == n {
					return true, nil
				}
			}
		}
		return false, nil
	}

	t := from.Truncate(time.Minute).Add(time.Minute)
	end := from.Add(366 * 24 * time.Hour)
	for t.Before(end) {
		minOK, _ := matchField(fields[0], t.Minute())
		hrOK, _ := matchField(fields[1], t.Hour())
		domOK, _ := matchField(fields[2], t.Day())
		monOK, _ := matchField(fields[3], int(t.Month()))
		dowOK, _ := matchField(fields[4], int(t.Weekday()))
		if minOK && hrOK && domOK && monOK && dowOK {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no occurrence found within one year for %q", expr)
}

func validateCronExpr(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("cron expression is required")
	}
	if _, err := CronNextOccurrence(s, time.Now()); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}

// PromptSchedule presents an interactive schedule picker and returns a 5-field
// cron expression. Returns ("", nil) for no schedule and ("", ErrBack) on cancel.
func PromptSchedule(current string) (string, error) {
	const noSched = "__none__"
	const custom = "__custom__"
	const back = "__back__"

	now := time.Now()

	type preset struct{ label, expr string }
	presets := []preset{
		{"Daily at midnight", "0 0 * * *"},
		{"Daily at noon", "0 12 * * *"},
		{"Daily at 8pm", "0 20 * * *"},
		{"Every Friday at 5pm", "0 17 * * 5"},
		{"Every Sunday at midnight", "0 0 * * 0"},
		{"Every weekday at 6pm", "0 18 * * 1-5"},
	}

	opts := make([]huh.Option[string], 0, len(presets)+3)
	for _, p := range presets {
		label := p.label
		if next, err := CronNextOccurrence(p.expr, now); err == nil {
			label += " · Next: " + next.Format("Jan 2 at 15:04")
		}
		opts = append(opts, huh.NewOption(label, p.expr))
	}
	opts = append(opts, huh.NewOption("Custom cron expression...", custom))
	opts = append(opts, huh.NewOption("No schedule", noSched))
	opts = append(opts, huh.NewOption("← Back", back))

	selected := current
	if err := NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Expiry schedule").
			Description("At execution time, the cluster's expiry is set to the next occurrence of this schedule").
			Options(opts...).
			Value(&selected),
	)).Run(); err != nil {
		return "", err
	}

	switch selected {
	case back:
		return "", ErrBack
	case noSched:
		return "", nil
	case custom:
		expr := current
		if err := NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Cron expression").
				Description("5 fields: minute hour day-of-month month day-of-week  (e.g. 0 20 * * 5)").
				Placeholder("0 20 * * 5").
				Value(&expr).
				Validate(validateCronExpr),
		)).Run(); err != nil {
			return "", err
		}
		return expr, nil
	default:
		return selected, nil
	}
}

// SelectFromList presents a huh.Select populated with the given names plus a
// "← Back" entry. Returns ErrBack when the user picks back.
func SelectFromList(title string, names []string, value *string) error {
	if len(names) == 0 {
		// Fall back to free-text when list is empty
		return NewForm(
			huh.NewGroup(
				huh.NewInput().Title(title).Value(value),
			),
		).Run()
	}

	opts := make([]huh.Option[string], 0, len(names)+1)
	for _, n := range names {
		opts = append(opts, huh.NewOption(n, n))
	}
	opts = append(opts, huh.NewOption("← Back", "__back__"))

	err := NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(value),
		),
	).Run()
	if err != nil {
		return err
	}
	if *value == "__back__" {
		return ErrBack
	}
	return nil
}
