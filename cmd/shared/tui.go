package shared

import (
	"errors"
	"strings"

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
