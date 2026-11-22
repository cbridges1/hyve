package logincmd

import (
	"testing"

	"github.com/cbridges1/hyve/internal/login"
)

func TestLoginURL(t *testing.T) {
	cfg := &login.Config{Current: "prod", Servers: []login.Server{
		{Name: "prod", URL: "https://hyve.example.com"},
		{Name: "lab", URL: "https://lab.example.com:8443"},
	}}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "https://hyve.example.com"},
		{[]string{"lab"}, "https://lab.example.com:8443"},
		{[]string{"other.example.com"}, "https://other.example.com"},
		{[]string{"https://new.example.com/"}, "https://new.example.com"},
	} {
		if got, err := loginURL(cfg, c.args); err != nil || got != c.want {
			t.Errorf("loginURL(%v) = %q, %v; want %q", c.args, got, err, c.want)
		}
	}
	if _, err := loginURL(&login.Config{}, nil); err == nil {
		t.Error("no URL and no current server: want an error")
	}
}
