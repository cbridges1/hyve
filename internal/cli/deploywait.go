package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/login"
	"github.com/cbridges1/hyve/internal/resources"
)

const (
	waitPoll = 3 * time.Second
	// problemGrace is how long a problem may last before a wait gives up:
	// Fleet retries, and a problem from before the change can still be
	// showing for a moment.
	problemGrace = 30 * time.Second
)

// waitFlags are --wait and --timeout, for the commands that change a
// Deployment.
type waitFlags struct {
	wait    bool
	timeout time.Duration
}

func (w *waitFlags) add(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&w.wait, "wait", false, "wait until every cluster it targets runs the change, and fail if one can't (for CI)")
	cmd.Flags().DurationVar(&w.timeout, "timeout", 10*time.Minute, "how long --wait waits")
}

// waitDeployment waits until every cluster a Deployment targets runs its
// latest change. It fails when the deadline passes, or a problem (a chart
// Fleet can't find, a cluster that can't apply it) lasts problemGrace.
func waitDeployment(ctx context.Context, s *session, kind, ns, name string, deadline time.Time) error {
	var last string
	var failing time.Time
	for {
		p, err := s.progress(ctx, kind, ns, name)
		if err != nil {
			return err
		}
		if p.Paused {
			return fmt.Errorf("%s/%s is paused: nothing changes on clusters until 'hyve deploy resume'", ns, name)
		}
		if p.Done() {
			if p.Desired == 0 {
				logf("%s/%s: it targets no clusters", ns, name)
			} else {
				logf("%s/%s: %s", ns, name, p.Summary())
			}
			return nil
		}
		line := p.Summary()
		if p.Problem != "" {
			line += ": " + oneLine(p.Problem)
		}
		if line != last {
			logf("%s/%s: %s", ns, name, line)
			last = line
		}
		switch {
		case p.Problem == "":
			failing = time.Time{}
		case failing.IsZero():
			failing = time.Now()
		case time.Since(failing) >= problemGrace:
			printProgress(os.Stderr, p)
			return fmt.Errorf("%s/%s failed: %s", ns, name, oneLine(p.Problem))
		}
		if time.Now().After(deadline) {
			printProgress(os.Stderr, p)
			return fmt.Errorf("%s/%s timed out: %s", ns, name, p.Summary())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPoll):
		}
	}
}

// progress reads a Deployment's progress. Signed in to a host, the host
// reads it (users can't read the BundleDeployments in clusters' Fleet
// namespaces); with --kubeconfig, it's read directly.
func (s *session) progress(ctx context.Context, kind, ns, name string) (delivery.Progress, error) {
	if s.server == nil {
		o, err := s.kube.Dynamic.Resource(resources.FleetGVRs[kind]).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return delivery.Progress{}, err
		}
		return delivery.Check(ctx, s.kube.Dynamic, o)
	}
	hc, err := login.HTTPClient(s.server.CACertFile, s.server.InsecureSkipVerify)
	if err != nil {
		return delivery.Progress{}, err
	}
	u := fmt.Sprintf("%s/v1/hyve/deployments/%s/%s/%s/progress", s.server.URL, url.PathEscape(ns), url.PathEscape(kind), url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return delivery.Progress{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.server.Token)
	resp, err := hc.Do(req)
	if err != nil {
		return delivery.Progress{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return delivery.Progress{}, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound && !strings.Contains(string(body), `"message"`):
		return delivery.Progress{}, errors.New("this hyve host is too old to report a Deployment's progress; upgrade it")
	case resp.StatusCode != http.StatusOK:
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			return delivery.Progress{}, errors.New(e.Message)
		}
		return delivery.Progress{}, fmt.Errorf("reading %s/%s's progress: %s", ns, name, resp.Status)
	}
	var p delivery.Progress
	return p, json.Unmarshal(body, &p)
}

// printProgress lists the clusters that don't run the change yet.
func printProgress(out io.Writer, p delivery.Progress) {
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	n := 0
	for _, c := range p.Clusters {
		if c.Done {
			continue
		}
		if n == 0 {
			_, _ = fmt.Fprintln(w, "CLUSTER\tSTATE\tMESSAGE")
		}
		n++
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", c.Cluster, c.State, dash(oneLine(c.Message)))
	}
	_ = w.Flush()
}

// then waits for a Deployment after a change, when --wait asks.
func (w *waitFlags) then(ctx context.Context, s *session, u *unstructured.Unstructured) error {
	if !w.wait {
		return nil
	}
	return waitDeployment(ctx, s, u.GetKind(), u.GetNamespace(), u.GetName(), time.Now().Add(w.timeout))
}

func deployWaitCmd() *cobra.Command {
	var ns string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait NAME",
		Short: "Wait until every cluster a Deployment targets runs its latest change",
		Long: `Waits until Fleet has taken in a Deployment's latest change (for a git
source, its latest commit) and every cluster it targets has applied it and
is ready. It fails (exits non-zero) when the timeout passes, or when the
Deployment or a cluster has an error that lasts 30 seconds; the clusters
that aren't done are listed. A paused Deployment fails at once.

create, update, apply, force and resume take --wait to do the same.`,
		Example: `  hyve deploy update web --version "$VERSION" && hyve deploy wait web --timeout 15m`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			it, err := s.findDeployment(cmd.Context(), args[0], ns)
			if err != nil {
				return err
			}
			return waitDeployment(cmd.Context(), s, it.Kind, it.Namespace, it.Name, time.Now().Add(timeout))
		},
	}
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "the Deployment's namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait")
	return cmd
}

func deployApplyCmd() *cobra.Command {
	var files []string
	var w waitFlags
	cmd := &cobra.Command{
		Use:   "apply -f FILE",
		Short: "Create or update Deployments (Fleet GitRepos, HelmOps, Bundles) and ClusterGroups from YAML",
		Long: `Creates or updates Deployments from YAML: Fleet GitRepos, HelmOps and
Bundles, and the ClusterGroups they target. It's the same whether they
exist or not, so a pipeline can run it every time. Objects without a
namespace go in the template namespace. '-f -' reads standard input.

The fields the file gives are set (server-side apply); fields it leaves out
that were set elsewhere (in the UI, with 'hyve deploy update') stay.`,
		Example: `  hyve deploy apply -f deployments/ --wait
  sed "s/VERSION/$VERSION/" web.yaml | hyve deploy apply -f - --wait --timeout 15m`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return applyManifests(cmd, files, applyDeployments, w)
		},
	}
	cmd.Flags().StringArrayVarP(&files, "filename", "f", nil, "YAML file, directory of .yaml/.yml/.json files, or - for standard input (repeatable)")
	_ = cmd.MarkFlagRequired("filename")
	w.add(cmd)
	return cmd
}
