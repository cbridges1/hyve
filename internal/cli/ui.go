package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/local"
	"github.com/cbridges1/hyve/internal/login"
)

// DefaultUIPort is where `hyve ui` serves a host whose certificate the
// browser doesn't trust (local mode's, a private CA's), if it's free. A
// fixed port keeps the page's origin, and what it remembers, the same.
const DefaultUIPort = 30445

// UICmd is `hyve ui`.
func UICmd() *cobra.Command {
	var o uiOptions
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the web UI for the current context, signed in",
		Long: `Opens hyve's web UI in the browser, signed in as you.

  - Local mode and standalone installs: hyve's own UI, served by the host.
    The browser gets a session of its own, made from the CLI's, so there's
    no password to type.
  - Rancher: Rancher's dashboard, at the hyve extension's Overview. Rancher
    signs you in itself.

When the host's certificate comes from a CA the browser doesn't trust
(local mode's, or a private one given to 'hyve login --ca-cert'), the UI is
served through this command at http://127.0.0.1, which runs until Ctrl-C.`,
		Example: `  hyve ui
  hyve ui --server local
  hyve ui --no-browser   # print the URL instead`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return openUI(cmd.Context(), o)
		},
	}
	o.flags(cmd)
	cmd.Flags().StringVar(&serverFlag, "server", "", "saved server to open (default: the current one; see 'hyve context list')")
	return cmd
}

func localUICmd() *cobra.Command {
	var o uiOptions
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open local mode's web UI, signed in (the same as 'hyve ui --server local')",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverFlag = local.ContextName
			return openUI(cmd.Context(), o)
		},
	}
	o.flags(cmd)
	return cmd
}

type uiOptions struct {
	noBrowser bool
	port      int
}

func (o *uiOptions) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	cmd.Flags().IntVar(&o.port, "port", DefaultUIPort, "local port for serving a host the browser doesn't trust (0 picks one)")
}

func openUI(ctx context.Context, o uiOptions) error {
	// newSession starts local mode if it's stopped, and keeps it from
	// idling while this runs.
	sess, err := newSession()
	if err != nil {
		return err
	}
	s := sess.server
	if s == nil {
		return errors.New("--kubeconfig has no UI; sign in to a hyve host or Rancher with 'hyve login'")
	}
	open := func(u string) error {
		if o.noBrowser {
			fmt.Println(u)
			return nil
		}
		if err := login.OpenBrowser(u); err != nil {
			logf("Couldn't open a browser (%v); open %s", err, u)
		}
		return nil
	}

	if s.Kind == login.KindRancher {
		return open(strings.TrimSuffix(s.URL, "/") + "/dashboard/hyve/c/_/hyve-overview")
	}

	hc, err := login.HTTPClient(s.CACertFile, s.InsecureSkipVerify)
	if err != nil {
		return err
	}
	browser, err := login.UISession(ctx, hc, s)
	if err != nil {
		return err
	}
	// The fragment never reaches a server: the page posts it to the host,
	// which sets its cookie, and drops it from the address bar.
	fragment := "#session=" + url.QueryEscape(browser.Token)

	if s.CACertFile == "" && !s.InsecureSkipVerify {
		return open(strings.TrimSuffix(s.URL, "/") + "/" + fragment)
	}
	return serveUI(ctx, o, s, hc, browser, open, fragment)
}

// serveUI serves the host at http://127.0.0.1:<port> until interrupted,
// trusting its CA here so the browser needn't.
func serveUI(ctx context.Context, o uiOptions, s *login.Server, hc *http.Client, browser login.Session, open func(string) error, fragment string) error {
	target, err := url.Parse(s.URL)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.port))
	if err != nil && o.port != 0 {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		Transport:     hc.Transport,
		FlushInterval: -1,
		// The browser reaches this over plain HTTP on loopback, so it
		// wouldn't keep a Secure cookie everywhere.
		ModifyResponse: func(resp *http.Response) error {
			cookies := resp.Header.Values("Set-Cookie")
			resp.Header.Del("Set-Cookie")
			for _, c := range cookies {
				resp.Header.Add("Set-Cookie", strings.ReplaceAll(c, "; Secure", ""))
			}
			return nil
		},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only this address: a page that rebinds its own name to
		// 127.0.0.1 doesn't get through.
		if r.Host != addr && r.Host != strings.Replace(addr, "127.0.0.1", "localhost", 1) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	base := "http://" + addr
	logf("hyve UI for %s at %s (Ctrl-C to stop)", s.Name, base)
	if err := open(base + "/" + fragment); err != nil {
		return err
	}
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	// The page can't be reached any more; end its session too.
	_ = login.Logout(shutdown, hc, &login.Server{URL: s.URL, Kind: s.Kind, Token: browser.Token})
	return nil
}
