package local

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cbridges1/hyve/internal/login"
)

// Resume makes the local cluster usable for a command: starts it if it's
// been stopped, and signs in again if the session has expired.
func Resume(ctx context.Context, home string, logf func(string, ...any)) error {
	ex, running := Running(ctx)
	if !ex || !ours(ctx, node(ctx)) {
		return errors.New("local mode isn't set up; run 'hyve local up'")
	}
	if !running {
		logf("Starting the local cluster")
		if err := startNode(ctx, node(ctx)); err != nil {
			return err
		}
	}
	cfg, err := login.Load(home)
	if err != nil {
		return err
	}
	s, err := cfg.Get(ContextName)
	if err != nil {
		return err
	}
	if !running || !s.SignedIn(time.Now()) {
		u, err := url.Parse(s.URL)
		if err != nil {
			return err
		}
		port, _ := strconv.Atoi(u.Port())
		pw, err := os.ReadFile(filepath.Join(Dir(home), "admin-password"))
		if err != nil {
			return err
		}
		if err := signIn(ctx, Options{Home: home, Port: port}, Dir(home), strings.TrimSpace(string(pw))); err != nil {
			return err
		}
	}
	if !running {
		// The host answers before its pods can reach the API server again.
		if err := waitAPI(ctx, home); err != nil {
			return err
		}
	}
	return nil
}

// waitAPI waits until the management cluster's API answers through the
// host endpoint.
func waitAPI(ctx context.Context, home string) error {
	cfg, err := login.Load(home)
	if err != nil {
		return err
	}
	s, err := cfg.Get(ContextName)
	if err != nil {
		return err
	}
	hc, err := login.HTTPClient(s.CACertFile, false)
	if err != nil {
		return err
	}
	return retry(ctx, 2*time.Minute, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.URL, "/")+"/k8s/clusters/"+login.LocalCluster+"/version", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+s.Token)
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("the local cluster's API isn't answering yet: %s", resp.Status)
		}
		return nil
	})
}
