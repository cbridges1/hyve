package local

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cbridges1/hyve/internal/login"
)

// DefaultIdle is how long the local cluster runs with no hyve command
// before it's stopped. Child clusters keep running; their agents retry
// until it's back.
const DefaultIdle = 5 * time.Minute

func lastUsedFile(home string) string   { return filepath.Join(Dir(home), "last-used") }
func watcherPIDFile(home string) string { return filepath.Join(Dir(home), "idle-watcher.pid") }

// Touch records that local mode was just used.
func Touch(home string) {
	_ = os.WriteFile(lastUsedFile(home), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
}

func idleFor(home string) time.Duration {
	fi, err := os.Stat(lastUsedFile(home))
	if err != nil {
		return 0
	}
	return time.Since(fi.ModTime())
}

// Resume makes the local cluster usable for a command: starts it if it's
// stopped, signs in again if the session has expired, records the use, and
// makes sure the idle watcher is running. It does nothing when local mode
// was never set up.
func Resume(ctx context.Context, home string, logf func(string, ...any)) error {
	ex, running := Running(ctx)
	if !ex || !ours(ctx) {
		return errors.New("local mode isn't set up; run 'hyve local up'")
	}
	if !running {
		logf("Starting the local cluster")
		if _, err := run(ctx, "k3d", "cluster", "start", Name, "--wait"); err != nil {
			return err
		}
	}
	Touch(home)
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
	return StartIdleWatcher(home, DefaultIdle)
}

// StartIdleWatcher starts `hyve local idle-stop` in the background, unless
// one is already running.
func StartIdleWatcher(home string, idle time.Duration) error {
	if b, err := os.ReadFile(watcherPIDFile(home)); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.Signal(0)) == nil {
				return nil
			}
		}
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(Dir(home), "idle-watcher.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(bin, "--home", home, "local", "idle-stop", "--after", idle.String())
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(watcherPIDFile(home), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// IdleStop stops the local cluster once it has gone unused for idle, then
// returns. It returns early if the cluster stops some other way.
func IdleStop(ctx context.Context, home string, idle time.Duration) error {
	defer os.Remove(watcherPIDFile(home))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
		if _, running := Running(ctx); !running {
			return nil
		}
		if d := idleFor(home); d >= idle {
			fmt.Printf("%s: idle for %s, stopping the local cluster\n", time.Now().Format(time.RFC3339), d.Round(time.Second))
			return Stop(ctx)
		}
	}
}
