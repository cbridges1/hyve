package install

import "testing"

func TestReleaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r/releases/v0.1.0/infrastructure-components.yaml":          "https://github.com/o/r/releases/download/v0.1.0/infrastructure-components.yaml",
		"https://github.com/o/r/releases/latest/infrastructure-components.yaml":          "https://github.com/o/r/releases/latest/download/infrastructure-components.yaml",
		"https://github.com/o/r/releases/download/v0.1.0/infrastructure-components.yaml": "https://github.com/o/r/releases/download/v0.1.0/infrastructure-components.yaml",
		"https://example.com/o/r/releases/v1/x.yaml":                                     "https://example.com/o/r/releases/v1/x.yaml",
	} {
		if got := releaseURL(in); got != want {
			t.Errorf("releaseURL(%s) = %s, want %s", in, got, want)
		}
	}
}
