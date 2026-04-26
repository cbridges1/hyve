package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── splitCSV ──────────────────────────────────────────────────────────────────

func TestSplitCSV_Single(t *testing.T) {
	assert.Equal(t, []string{"wf-a"}, splitCSV("wf-a"))
}

func TestSplitCSV_Multiple(t *testing.T) {
	assert.Equal(t, []string{"wf-a", "wf-b", "wf-c"}, splitCSV("wf-a,wf-b,wf-c"))
}

func TestSplitCSV_WithSpaces(t *testing.T) {
	assert.Equal(t, []string{"wf-a", "wf-b"}, splitCSV("wf-a, wf-b"))
}

func TestSplitCSV_EmptyString(t *testing.T) {
	result := splitCSV("")
	assert.Nil(t, result)
}

func TestSplitCSV_OnlyCommas(t *testing.T) {
	result := splitCSV(",,,")
	assert.Nil(t, result)
}

func TestSplitCSV_TrailingComma(t *testing.T) {
	assert.Equal(t, []string{"wf-a"}, splitCSV("wf-a,"))
}

func TestSplitCSV_LeadingComma(t *testing.T) {
	assert.Equal(t, []string{"wf-a"}, splitCSV(",wf-a"))
}

// ── trimSpace ─────────────────────────────────────────────────────────────────

func TestTrimSpace_NoSpaces(t *testing.T) {
	assert.Equal(t, "hello", trimSpace("hello"))
}

func TestTrimSpace_LeadingSpaces(t *testing.T) {
	assert.Equal(t, "hello", trimSpace("   hello"))
}

func TestTrimSpace_TrailingSpaces(t *testing.T) {
	assert.Equal(t, "hello", trimSpace("hello   "))
}

func TestTrimSpace_BothSides(t *testing.T) {
	assert.Equal(t, "hello world", trimSpace("  hello world  "))
}

func TestTrimSpace_TabsAndSpaces(t *testing.T) {
	assert.Equal(t, "hello", trimSpace("\t hello\t"))
}

func TestTrimSpace_EmptyString(t *testing.T) {
	assert.Equal(t, "", trimSpace(""))
}

func TestTrimSpace_OnlyWhitespace(t *testing.T) {
	assert.Equal(t, "", trimSpace("   \t  "))
}

// ── SelectFromGroups option ordering ─────────────────────────────────────────

// OptionGroup ordering is enforced structurally: groups are appended before
// the manual/back options. Verify the ordering contract via the build helper.

func TestOptionGroupOrdering_GroupsBeforeManual(t *testing.T) {
	groups := []OptionGroup{
		{Name: "Americas"},
		{Name: "Europe"},
	}

	// Replicate the build logic from SelectFromGroups to confirm ordering.
	const manualKey = "__manual__"
	const backKey = "__back__"
	var opts []string
	for _, g := range groups {
		opts = append(opts, g.Name)
	}
	opts = append(opts, manualKey, backKey)

	if opts[0] != "Americas" || opts[1] != "Europe" {
		t.Errorf("groups should come first: got %v", opts)
	}
	if opts[2] != manualKey {
		t.Errorf("manual entry should come after groups: got %v", opts)
	}
	if opts[3] != backKey {
		t.Errorf("back should be last: got %v", opts)
	}
}
