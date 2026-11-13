package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfirmName(t *testing.T) {
	for _, tc := range []struct {
		in  string
		yes bool
		ok  bool
	}{
		{"web\n", false, true},
		{"  web  \n", false, true},
		{"we\n", false, false},
		{"", false, false},
		{"", true, true},
	} {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader(tc.in))
		cmd.SetErr(&strings.Builder{})
		if err := confirmName(cmd, "web", "release it", tc.yes); (err == nil) != tc.ok {
			t.Errorf("%q yes=%v: %v", tc.in, tc.yes, err)
		}
	}
}
