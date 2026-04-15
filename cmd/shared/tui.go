package shared

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

// PromptExpiresAt presents a user-friendly date picker and returns an RFC 3339
// expiry timestamp, or "" if the user chooses no expiry. Pass the existing
// value (or "") to pre-populate the custom path.
func PromptExpiresAt(current string) (string, error) {
	const (
		keyNone   = "__none__"
		key1w     = "__1w__"
		key2w     = "__2w__"
		key1m     = "__1m__"
		key3m     = "__3m__"
		key6m     = "__6m__"
		key1y     = "__1y__"
		keyCustom = "__custom__"
	)

	now := time.Now().UTC()
	dateFmt := "Jan 2, 2006"

	presets := []huh.Option[string]{
		huh.NewOption("No expiry", keyNone),
		huh.NewOption(fmt.Sprintf("1 week   · %s", now.AddDate(0, 0, 7).Format(dateFmt)), key1w),
		huh.NewOption(fmt.Sprintf("2 weeks  · %s", now.AddDate(0, 0, 14).Format(dateFmt)), key2w),
		huh.NewOption(fmt.Sprintf("1 month  · %s", now.AddDate(0, 1, 0).Format(dateFmt)), key1m),
		huh.NewOption(fmt.Sprintf("3 months · %s", now.AddDate(0, 3, 0).Format(dateFmt)), key3m),
		huh.NewOption(fmt.Sprintf("6 months · %s", now.AddDate(0, 6, 0).Format(dateFmt)), key6m),
		huh.NewOption(fmt.Sprintf("1 year   · %s", now.AddDate(1, 0, 0).Format(dateFmt)), key1y),
		huh.NewOption("Custom date...", keyCustom),
	}

	// Default selection: keep existing value shown as custom, otherwise no expiry.
	choice := keyNone
	if current != "" {
		choice = keyCustom
	}

	if err := NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Expiry").
				Description("The cluster will be automatically deleted after this date.").
				Options(presets...).
				Value(&choice),
		),
	).Run(); err != nil {
		return "", err
	}

	// Resolve preset choices directly to timestamps.
	switch choice {
	case keyNone:
		return "", nil
	case key1w:
		return now.AddDate(0, 0, 7).Truncate(24 * time.Hour).Format(time.RFC3339), nil
	case key2w:
		return now.AddDate(0, 0, 14).Truncate(24 * time.Hour).Format(time.RFC3339), nil
	case key1m:
		return now.AddDate(0, 1, 0).Truncate(24 * time.Hour).Format(time.RFC3339), nil
	case key3m:
		return now.AddDate(0, 3, 0).Truncate(24 * time.Hour).Format(time.RFC3339), nil
	case key6m:
		return now.AddDate(0, 6, 0).Truncate(24 * time.Hour).Format(time.RFC3339), nil
	case key1y:
		return now.AddDate(1, 0, 0).Truncate(24 * time.Hour).Format(time.RFC3339), nil
	}

	// Custom date path — pre-populate from current value when available.
	yearStr := fmt.Sprintf("%d", now.Year()+1)
	monthStr := fmt.Sprintf("%02d", int(now.Month()))
	dayStr := "01"
	timeOfDay := "00:00"

	if current != "" {
		if t, err := time.Parse(time.RFC3339, current); err == nil {
			yearStr = fmt.Sprintf("%d", t.Year())
			monthStr = fmt.Sprintf("%02d", int(t.Month()))
			dayStr = fmt.Sprintf("%02d", t.Day())
		}
	}

	// Year options: current year through current+5.
	yearOpts := make([]huh.Option[string], 6)
	for i := range yearOpts {
		y := fmt.Sprintf("%d", now.Year()+i)
		yearOpts[i] = huh.NewOption(y, y)
	}

	monthOpts := []huh.Option[string]{
		huh.NewOption("January", "01"), huh.NewOption("February", "02"),
		huh.NewOption("March", "03"), huh.NewOption("April", "04"),
		huh.NewOption("May", "05"), huh.NewOption("June", "06"),
		huh.NewOption("July", "07"), huh.NewOption("August", "08"),
		huh.NewOption("September", "09"), huh.NewOption("October", "10"),
		huh.NewOption("November", "11"), huh.NewOption("December", "12"),
	}

	timeOpts := []huh.Option[string]{
		huh.NewOption("Start of day  00:00 UTC", "00:00"),
		huh.NewOption("Noon          12:00 UTC", "12:00"),
		huh.NewOption("End of day    23:59 UTC", "23:59"),
	}

	if err := NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Year").Options(yearOpts...).Value(&yearStr),
			huh.NewSelect[string]().Title("Month").Options(monthOpts...).Value(&monthStr),
			huh.NewInput().
				Title("Day").
				Placeholder("01").
				Validate(func(s string) error {
					d, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || d < 1 || d > 31 {
						return errors.New("enter a day between 1 and 31")
					}
					return nil
				}).
				Value(&dayStr),
			huh.NewSelect[string]().Title("Time (UTC)").Options(timeOpts...).Value(&timeOfDay),
		),
	).Run(); err != nil {
		return "", err
	}

	// Normalise day to two digits.
	if d, err := strconv.Atoi(strings.TrimSpace(dayStr)); err == nil {
		dayStr = fmt.Sprintf("%02d", d)
	}

	raw := fmt.Sprintf("%s-%s-%sT%s:00Z", yearStr, monthStr, dayStr, timeOfDay)
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", fmt.Errorf("invalid date combination: %w", err)
	}
	return t.Format(time.RFC3339), nil
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
