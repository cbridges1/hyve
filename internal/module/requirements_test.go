package module

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateToolRequirements(t *testing.T) {
	t.Run("no requirements always passes", func(t *testing.T) {
		assert.NoError(t, ValidateToolRequirements(nil))
		assert.NoError(t, ValidateToolRequirements([]ToolRequirement{}))
	})

	t.Run("a tool that's definitely on PATH passes", func(t *testing.T) {
		// "sh" is present on every runner this test suite runs on (macOS, Linux CI).
		err := ValidateToolRequirements([]ToolRequirement{{Name: "sh"}})
		assert.NoError(t, err)
	})

	t.Run("a tool that can't possibly exist fails with a clear message", func(t *testing.T) {
		err := ValidateToolRequirements([]ToolRequirement{
			{Name: "definitely-not-a-real-binary-xyz123", Description: "needed for widgets"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "definitely-not-a-real-binary-xyz123")
		assert.Contains(t, err.Error(), "needed for widgets")
	})

	t.Run("reports every missing tool, not just the first", func(t *testing.T) {
		err := ValidateToolRequirements([]ToolRequirement{
			{Name: "sh"}, // present — should not appear in the error
			{Name: "not-a-real-binary-one"},
			{Name: "not-a-real-binary-two"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not-a-real-binary-one")
		assert.Contains(t, err.Error(), "not-a-real-binary-two")
		assert.NotContains(t, err.Error(), "'sh' not found")
	})
}
