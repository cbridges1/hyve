// Package host is `hyve host`: hyve's host endpoint, the server that stands
// in for Rancher's when Rancher isn't installed (HYVE-PROPOSAL.md, "hyve as
// the Rancher host"): sign-in, the management cluster's API at
// /k8s/clusters/local, the tunnel child clusters' cattle-cluster-agents
// dial (/v3/connect), their APIs through it at /k8s/clusters/<id>, and
// hyve's own UI at / (--ui).
package host

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/hostapi"
	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/signin"
	"github.com/cbridges1/hyve/internal/tunnel"
	"github.com/cbridges1/hyve/internal/webui"
)

// Version is reported by /v1-public/hyve; set by the root command.
var Version = "dev"

var (
	listenAddr      string
	tlsCertFile     string
	tlsKeyFile      string
	namespace       string
	sessionTTL      time.Duration
	bootstrapSecret string
	caCertsFile     string
	localUser       string
	serveUI         bool
)

// Cmd is `hyve host`.
var Cmd = &cobra.Command{
	Use:   "host",
	Short: "hyve's host endpoint (standalone installs, without Rancher)",
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Serve sign-in, the agent tunnel and the /k8s/clusters proxy",
	Long: `Runs hyve's host endpoint. Users sign in with a username and password
(hyve.io User objects; password hashes and sessions are Secrets in
--namespace), and the CLI and UI reach the management cluster through
/k8s/clusters/local as the signed-in user, impersonated as hyve:<username>.

On first start it creates the User "admin", with the password in the
--bootstrap-secret Secret's bootstrapPassword key (generated if the Secret
doesn't exist). The password must be changed at first sign-in.

Run it in the cluster it manages; it uses the in-cluster config, or
KUBECONFIG outside a cluster.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd.Context())
	},
}

func init() {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		ns = "hyve-system"
	}
	runCmd.Flags().StringVar(&listenAddr, "listen", ":8443", "address to listen on")
	runCmd.Flags().StringVar(&tlsCertFile, "tls-cert", "", "TLS certificate file (serves plain HTTP when unset, for use behind a TLS-terminating ingress)")
	runCmd.Flags().StringVar(&tlsKeyFile, "tls-key", "", "TLS key file")
	runCmd.Flags().StringVar(&namespace, "namespace", ns, "namespace holding password and session Secrets (default: $POD_NAMESPACE or hyve-system)")
	runCmd.Flags().DurationVar(&sessionTTL, "session-ttl", signin.DefaultSessionTTL, "how long a sign-in lasts")
	runCmd.Flags().StringVar(&bootstrapSecret, "bootstrap-secret", "bootstrap-secret", "Secret holding the first admin's bootstrapPassword")
	runCmd.Flags().BoolVar(&serveUI, "ui", false, "serve hyve's own UI at / (built into the binary with 'task ui')")
	runCmd.Flags().StringVar(&localUser, "local-user", "", "sign browsers at 127.0.0.1 or localhost in as this user without a password (local mode only: never on a host others can reach)")
	runCmd.Flags().StringVar(&caCertsFile, "cacerts", "", "PEM CA the host's certificate chains to, for agents to pin (leave unset for a publicly trusted certificate)")
	Cmd.AddCommand(runCmd)
}

func run(ctx context.Context) error {
	if (tlsCertFile == "") != (tlsKeyFile == "") {
		return errors.New("--tls-cert and --tls-key go together")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading the cluster config: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := hyvev1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating a client: %w", err)
	}

	store := &signin.Store{Client: c, Namespace: namespace, SessionTTL: sessionTTL}
	if err := store.EnsureBootstrapAdmin(ctx, bootstrapSecret); err != nil {
		return fmt.Errorf("creating the bootstrap admin: %w", err)
	}
	proxy, err := signin.NewLocalProxy(cfg, store)
	if err != nil {
		return fmt.Errorf("building the /k8s/clusters/local proxy: %w", err)
	}
	srv := signin.NewServer(store, proxy, Version)
	srv.LocalUser = localUser
	if localUser != "" {
		log.Printf("host: browsers at 127.0.0.1 or localhost are signed in as %s without a password", localUser)
	}
	caCerts := ""
	if caCertsFile != "" {
		b, err := os.ReadFile(caCertsFile)
		if err != nil {
			return err
		}
		caCerts = string(b)
	}
	tun := tunnel.NewServer(&tunnel.Registry{Client: c, Namespace: namespace}, caCerts)
	children := &tunnel.Proxy{Server: tun, Authenticate: func(r *http.Request) (tunnel.Identity, bool) {
		return srv.SessionIdentity(r)
	}, Direct: func(ctx context.Context, id string) (*rest.Config, error) {
		// <environment>.<name>: the CAPI kubeconfig, for clusters whose
		// agent can't reach this host (push delivery).
		ns, name, ok := strings.Cut(id, ".")
		if !ok {
			return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "clusters"}, id)
		}
		var sec corev1.Secret
		if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name + "-kubeconfig"}, &sec); err != nil {
			return nil, err
		}
		return clientcmd.RESTConfigFromKubeConfig(sec.Data["value"])
	}}
	api := &hostapi.API{Config: cfg, Identify: srv.SessionIdentity, DefaultTemplateNamespace: install.DefaultTemplateNamespace}
	srv.Routes = func(mux *http.ServeMux) {
		tun.Register(mux)
		mux.Handle("/k8s/clusters/", children)
		api.Register(mux)
	}
	if serveUI {
		if !webui.Built() {
			log.Printf("host: --ui is set, but this binary was built without the UI; / says so")
		}
		srv.UI = webui.Handler()
	}
	go srv.RunSessionCleanup(ctx, 10*time.Minute)

	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("host: listening on %s (namespace %s)", listenAddr, namespace)
		if tlsCertFile != "" {
			errCh <- httpSrv.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
		} else {
			log.Printf("host: serving plain HTTP; put a TLS-terminating ingress in front")
			errCh <- httpSrv.ListenAndServe()
		}
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}
