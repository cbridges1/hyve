package module

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLocalSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"./custom-modules/civo", true},
		{"/absolute/path/to/module", true},
		{"github.com/hyve-modules/civo", false},
		{"github.com/hyve-modules/civo@v1.0.0", false},
		{"github.com/org/repo//path/to/module", false},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			assert.Equal(t, tt.want, IsLocalSource(tt.source))
		})
	}
}
