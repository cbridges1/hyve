package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/hostapi"
	"github.com/cbridges1/hyve/internal/login"
	"github.com/cbridges1/hyve/internal/release"
)

func clusterReleaseCmd() *cobra.Command {
	var (
		saveKubeconfig string
		force, dryRun  bool
		yes            bool
	)
	cmd := &cobra.Command{
		Use:   "release NAME",
		Short: "Take a cluster out of hyve's management and leave it running",
		Long: `Releases a cluster: hyve stops managing it, and the cluster and what
runs on it are left alone. The reverse of 'hyve cluster adopt' and
'hyve cluster import'.

For a cluster hyve made or adopted, its Cluster API objects are paused, so
its provider stops acting on it, then removed without the provider
deleting anything: the cluster keeps running and is billed by its
provider as before. hyve's and Fleet's agents are removed from the
cluster first, so what Fleet deployed there stays, unmanaged. Released
from an adopted cluster or not, 'hyve cluster adopt' or 'hyve cluster
import' brings it back.

For an imported cluster this is 'hyve cluster delete': hyve detaches it.

You'll lose hyve's kubeconfig for it: --save-kubeconfig keeps a copy (the
provider's own, such as 'civo kubernetes config', works too).

Asks you to type the cluster's name; --yes doesn't (scripts).`,
		Example: `  hyve cluster release web -e prod --save-kubeconfig web.yaml
  hyve cluster release web -e prod --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ns, name := s.env(), args[0]
			c, err := s.getCluster(ctx, name)
			if err != nil {
				return err
			}
			imported := clusters.Origin(c) == clusters.OriginImported
			if !dryRun {
				what := "release it (it keeps running; hyve stops managing it)"
				if imported {
					what = "detach it (it keeps running)"
				}
				if err := confirmName(cmd, name, what, yes); err != nil {
					return err
				}
			}
			res, err := s.release(ctx, ns, name, hostapi.ReleaseRequest{Force: force, DryRun: dryRun})
			if err != nil {
				return err
			}
			if dryRun {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Releasing %s would remove from hyve (nothing on the cluster but hyve's and Fleet's agents):\n", name)
				for _, r := range res.Removed {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  "+r)
				}
				return nil
			}
			if saveKubeconfig != "" && len(res.Kubeconfig) > 0 {
				if err := os.WriteFile(saveKubeconfig, res.Kubeconfig, 0o600); err != nil {
					return fmt.Errorf("the cluster is released, but its kubeconfig couldn't be saved: %w", err)
				}
				logf("Saved its kubeconfig to %s", saveKubeconfig)
			}
			if res.DetachError != "" {
				logf("hyve's and Fleet's agents are still on the cluster, disconnected: %s", res.DetachError)
			}
			if res.Imported {
				logf("Detaching imported cluster %s in environment %s (the cluster itself is left running)", name, ns)
			} else {
				logf("Released cluster %s: it keeps running, and hyve no longer manages it", name)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&saveKubeconfig, "save-kubeconfig", "", "write the cluster's kubeconfig to this file before hyve forgets it")
	f.BoolVar(&force, "force", false, "release it even if its agents can't be removed (they stay, disconnected)")
	f.BoolVar(&dryRun, "dry-run", false, "check you may release it and list what would be removed")
	f.BoolVarP(&yes, "yes", "y", false, "don't ask to type the cluster's name")
	return cmd
}

// confirmName has the user type a cluster's name before what would be
// hard to undo; --yes skips it, and without a terminal it's required.
func confirmName(cmd *cobra.Command, name, what string, yes bool) error {
	if yes {
		return nil
	}
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		return fmt.Errorf("give --yes to %s without typing its name", what)
	}
	return confirmFrom(cmd, in, name, what)
}

func confirmFrom(cmd *cobra.Command, r io.Reader, name, what string) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Type the cluster's name (%s) to %s: ", name, what)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(line) != name {
		return errors.New("the name didn't match; nothing was changed")
	}
	return nil
}

// release releases a cluster: through the host when signed in to one (it
// reaches the cluster to remove the agents), or directly with
// --kubeconfig.
func (s *session) release(ctx context.Context, ns, name string, req hostapi.ReleaseRequest) (*release.Result, error) {
	if s.server == nil {
		return release.Release(ctx, s.kube, ns, name, release.Options{Force: req.Force, DryRun: req.DryRun, Logf: logf})
	}
	var res release.Result
	err := hostPost(ctx, s.server, fmt.Sprintf("/v1/hyve/clusters/%s/%s/release", ns, name), req, &res)
	return &res, err
}

// hostPost posts body to the host's path as JSON and reads its answer
// into out.
func hostPost(ctx context.Context, server *login.Server, path string, body, out any) error {
	hc, err := login.HTTPClient(server.CACertFile, server.InsecureSkipVerify)
	if err != nil {
		return err
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+server.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return errors.New(e.Message)
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			return errors.New("this hyve host is too old for this; upgrade it")
		}
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return json.Unmarshal(data, out)
}
