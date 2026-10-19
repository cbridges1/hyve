// Package local is hyve's local mode (HYVE-PROPOSAL.md, "Local mode"): a
// standalone install on a K3s cluster in Docker that the CLI creates,
// stops and removes itself, so laptops and CI get the same templates with
// no server to run. It runs on Docker's "kind" network, where CAPD puts its
// clusters, so their agents reach the host endpoint directly.
package local

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/login"
)

// Defaults.
const (
	Name          = "hyve"
	DockerNetwork = "kind"
	DefaultPort   = 30444
	DefaultK3s    = "rancher/k3s:v1.34.12-k3s1"
	// ContextName is the built-in context local mode signs in as.
	ContextName = "local"
)

// Options for Up.
type Options struct {
	Home string
	// Port is the host port (and node port) the host endpoint listens on.
	Port     int
	K3sImage string
	// Image is hyve's image; a local Docker image is imported into the
	// cluster first.
	Image string
	// PublicURL is the host endpoint's URL for children that can't reach
	// Docker's kind network (a tunnel you run); default the node's address.
	PublicURL string
	// Providers are added on first start: CAPD and kubeadm by default.
	Providers []string
	// Tuning, for memory (open question 5): GOGC for K3s and every
	// controller (default 40), and the full install (CAPI Operator and
	// cert-manager) instead of the lean one.
	GOGC         string
	Full         bool
	APIServerArg []string
	Log          func(format string, args ...any)
}

func (o *Options) defaults() {
	if o.Port == 0 {
		o.Port = DefaultPort
	}
	if o.K3sImage == "" {
		o.K3sImage = DefaultK3s
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.GOGC == "" {
		o.GOGC = "40"
	}
}

// Dir is local mode's state directory, ~/.hyve/local.
func Dir(home string) string { return filepath.Join(home, "local") }

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func exists(ctx context.Context) bool {
	out, err := run(ctx, "k3d", "cluster", "list", "--no-headers")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == Name {
			return true
		}
	}
	return false
}

func nodeName() string { return "k3d-" + Name + "-server-0" }

const ownerLabel = "hyve.io/local=true"

// ours reports whether the k3d cluster's node carries local mode's label.
func ours(ctx context.Context) bool {
	out, err := run(ctx, "docker", "inspect", "-f", `{{ index .Config.Labels "hyve.io/local" }}`, nodeName())
	return err == nil && out == "true"
}

// Running reports whether the local cluster exists and its node is up.
func Running(ctx context.Context) (exists_, running bool) {
	if !exists(ctx) {
		return false, false
	}
	out, err := run(ctx, "docker", "inspect", "-f", "{{.State.Running}}", nodeName())
	return true, err == nil && out == "true"
}

// Up creates (or starts) the local cluster, installs hyve in it and signs
// the CLI in as its admin. Safe to re-run.
func Up(ctx context.Context, o Options) error {
	o.defaults()
	for _, bin := range []string{"docker", "k3d"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found on PATH: local mode needs Docker and k3d", bin)
		}
	}
	if o.Image == "" && install.IsDevBuild() {
		// A build from source has no published image: use the one the
		// checkout builds, before creating anything.
		if _, err := run(ctx, "docker", "image", "inspect", install.DevImage); err != nil {
			return fmt.Errorf("%w\n  (or run 'task local', which does both)", install.ErrNoImage)
		}
		o.Image = install.DevImage
	}
	dir := Dir(o.Home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := run(ctx, "docker", "network", "inspect", DockerNetwork); err != nil {
		if _, err := run(ctx, "docker", "network", "create", DockerNetwork); err != nil {
			return err
		}
	}
	if ex, _ := Running(ctx); ex && !ours(ctx) {
		return fmt.Errorf("a k3d cluster named %q exists that local mode didn't create; rename or delete it", Name)
	}
	if ex, running := Running(ctx); !ex {
		o.Log("Creating the local cluster (%s)", o.K3sImage)
		args := []string{"cluster", "create", Name,
			"--image", o.K3sImage,
			"--network", DockerNetwork,
			"--no-lb",
			"--port", fmt.Sprintf("%d:%d@server:0:direct", o.Port, o.Port),
			// CAPD creates child-cluster nodes as containers on this Docker.
			"--volume", "/var/run/docker.sock:/var/run/docker.sock@server:0",
			"--kubeconfig-update-default=false",
			// Marks the node as local mode's, so it never takes over a k3d
			// cluster it didn't create.
			"--runtime-label", ownerLabel + "@server:0",
			"--wait", "--timeout", "5m",
		}
		for _, d := range []string{"traefik", "metrics-server", "servicelb"} {
			args = append(args, "--k3s-arg", "--disable="+d+"@server:0")
		}
		for _, a := range o.APIServerArg {
			args = append(args, "--k3s-arg", "--kube-apiserver-arg="+a+"@server:0")
		}
		if o.GOGC != "" {
			args = append(args, "--env", "GOGC="+o.GOGC+"@server:0")
		}
		if _, err := run(ctx, "k3d", args...); err != nil {
			return err
		}
	} else if !running {
		o.Log("Starting the local cluster")
		if _, err := run(ctx, "k3d", "cluster", "start", Name, "--wait"); err != nil {
			return err
		}
	}
	kcRaw, err := run(ctx, "k3d", "kubeconfig", "get", Name)
	if err != nil {
		return err
	}
	kcPath := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kcPath, []byte(kcRaw+"\n"), 0o600); err != nil {
		return err
	}
	ip, err := run(ctx, "docker", "inspect", nodeName(), "--format", fmt.Sprintf(`{{ (index .NetworkSettings.Networks %q).IPAddress }}`, DockerNetwork))
	if err != nil || ip == "" {
		return fmt.Errorf("can't find the local cluster's address on the %q network: %v", DockerNetwork, err)
	}
	// Children on Docker's network reach the host by the node's container
	// name, which Docker's DNS resolves; its address changes when the node
	// restarts.
	hostURL := o.PublicURL
	if hostURL == "" {
		hostURL = fmt.Sprintf("https://%s:%d", nodeName(), o.Port)
	}
	caPEM, certPEM, keyPEM, err := ensurePKI(dir, ip)
	if err != nil {
		return err
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kcRaw))
	if err != nil {
		return err
	}
	kc, err := kube.New(cfg)
	if err != nil {
		return err
	}
	if o.Image != "" {
		if _, err := run(ctx, "docker", "image", "inspect", o.Image); err == nil {
			o.Log("Importing %s", o.Image)
			if _, err := run(ctx, "k3d", "image", "import", "-c", Name, o.Image); err != nil {
				return err
			}
		}
	}
	password, err := ensureSecretFile(filepath.Join(dir, "admin-password"))
	if err != nil {
		return err
	}
	ns := "hyve-system"
	for _, obj := range []*unstructured.Unstructured{
		{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}},
		{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls",
			"metadata":   map[string]any{"name": "hyve-host-tls", "namespace": ns},
			"stringData": map[string]any{"tls.crt": string(certPEM), "tls.key": string(keyPEM)}}},
		// The bootstrap admin's first password; local mode changes it at
		// its first sign-in below, to the one it keeps.
		{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
			"metadata":   map[string]any{"name": "bootstrap-secret", "namespace": ns},
			"stringData": map[string]any{"bootstrapPassword": "hyve-local-" + password}}},
	} {
		if err := kc.ApplyObject(ctx, obj); err != nil {
			return err
		}
	}
	pullPolicy := ""
	if o.Image != "" {
		pullPolicy = "IfNotPresent"
	}
	if _, err := install.Install(ctx, kc, install.Options{
		Namespace: ns, Image: o.Image, PullPolicy: pullPolicy,
		HostURL: hostURL, HostTLSSecret: "hyve-host-tls", HostCACerts: string(caPEM),
		Lean: !o.Full, GOGC: o.GOGC,
		Values: map[string]any{"host": map[string]any{"service": map[string]any{"type": "NodePort", "nodePort": o.Port}}},
		Log:    o.Log,
	}); err != nil {
		return err
	}
	if err := signIn(ctx, o, dir, password); err != nil {
		return err
	}
	if err := addProviders(ctx, kc, o); err != nil {
		return err
	}
	Touch(o.Home)
	return StartIdleWatcher(o.Home, DefaultIdle)
}

// signIn saves the "local" context, signed in as admin.
func signIn(ctx context.Context, o Options, dir, password string) error {
	caFile := filepath.Join(dir, "ca.crt")
	base := fmt.Sprintf("https://127.0.0.1:%d", o.Port)
	hc, err := login.HTTPClient(caFile, false)
	if err != nil {
		return err
	}
	// Wait for the host to answer before trying passwords: failed sign-ins
	// count towards its lockout.
	if err := retry(ctx, 2*time.Minute, func() error {
		_, err := login.Detect(ctx, hc, base)
		return err
	}); err != nil {
		return fmt.Errorf("the local host endpoint isn't answering: %w", err)
	}
	// The kept password, or on a fresh install the bootstrap one, changed
	// to the kept one.
	sess, err := login.PasswordLogin(ctx, hc, base, "localProvider", "admin", password, "")
	if err != nil {
		sess, err = login.PasswordLogin(ctx, hc, base, "localProvider", "admin", "hyve-local-"+password, password)
	}
	if err != nil {
		return fmt.Errorf("signing in to the local host: %w", err)
	}
	cfg, err := login.Load(o.Home)
	if err != nil {
		return err
	}
	s := login.Server{Name: ContextName, URL: base, Kind: login.KindHyve, Provider: "localProvider", Username: "admin", CACertFile: caFile}
	if old := cfg.FindByURL(base); old != nil {
		s.Environment, s.TemplateNamespace = old.Environment, old.TemplateNamespace
	}
	s.Token, s.ExpiresAt = sess.Token, sess.ExpiresAt
	cfg.Put(s)
	if cfg.Current == "" {
		cfg.Current = ContextName
	}
	return cfg.Save()
}

func retry(ctx context.Context, timeout time.Duration, f func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// addProviders adds the default providers through the CAPI Operator.
func addProviders(ctx context.Context, kc *kube.Client, o Options) error {
	providers := o.Providers
	if providers == nil {
		providers = []string{"docker"}
	}
	if !o.Full {
		return install.LeanInstall(ctx, kc, providers, install.LeanOptions{GOGC: o.GOGC, Log: o.Log})
	}
	for _, p := range providers {
		objs, err := install.ProviderObjects(p)
		if err != nil {
			return err
		}
		for _, obj := range objs {
			if err := kc.ApplyObject(ctx, obj); err != nil {
				return err
			}
		}
		o.Log("Added Cluster API provider %s", p)
	}
	return nil
}

// Stop stops the local cluster's node; child clusters keep running.
func Stop(ctx context.Context) error {
	if !ours(ctx) {
		return fmt.Errorf("no local-mode cluster to stop")
	}
	_, err := run(ctx, "k3d", "cluster", "stop", Name)
	return err
}

// Down deletes the local cluster. CAPD children are containers of their
// own; delete them first ('hyve cluster delete'), or remove them with
// removeChildren.
func Down(ctx context.Context, home string) error {
	if exists(ctx) {
		if !ours(ctx) {
			return fmt.Errorf("the k3d cluster %q isn't local mode's; not deleting it", Name)
		}
		if _, err := run(ctx, "k3d", "cluster", "delete", Name); err != nil {
			return err
		}
	}
	cfg, err := login.Load(home)
	if err != nil {
		return err
	}
	if cfg.Remove(ContextName) {
		return cfg.Save()
	}
	return nil
}

func ensureSecretFile(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 12 {
		return strings.TrimSpace(string(b)), nil
	}
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	pw := hex.EncodeToString(b)
	return pw, os.WriteFile(path, []byte(pw+"\n"), 0o600)
}

// ensurePKI keeps a local CA and a host certificate for the node's
// address, localhost and 127.0.0.1 in dir.
func ensurePKI(dir, ip string) (caPEM, certPEM, keyPEM []byte, err error) {
	caCrt, caKeyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	hostCrt, hostKey := filepath.Join(dir, "host.crt"), filepath.Join(dir, "host.key")
	caPEM, err1 := os.ReadFile(caCrt)
	caKeyPEM, err2 := os.ReadFile(caKeyPath)
	if err1 != nil || err2 != nil {
		caPEM, caKeyPEM, err = newCert(nil, nil, "hyve-local-ca", nil)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := os.WriteFile(caCrt, caPEM, 0o600); err != nil {
			return nil, nil, nil, err
		}
		if err := os.WriteFile(caKeyPath, caKeyPEM, 0o600); err != nil {
			return nil, nil, nil, err
		}
	}
	certPEM, err1 = os.ReadFile(hostCrt)
	keyPEM, err2 = os.ReadFile(hostKey)
	if err1 == nil && err2 == nil && certCovers(certPEM, ip) {
		return caPEM, certPEM, keyPEM, nil
	}
	certPEM, keyPEM, err = newCert(caPEM, caKeyPEM, "hyve-local-host", []string{ip, "127.0.0.1", "localhost", nodeName()})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := os.WriteFile(hostCrt, certPEM, 0o600); err != nil {
		return nil, nil, nil, err
	}
	return caPEM, certPEM, keyPEM, os.WriteFile(hostKey, keyPEM, 0o600)
}

func certCovers(certPEM []byte, ip string) bool {
	b, _ := pem.Decode(certPEM)
	if b == nil {
		return false
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil || time.Now().After(c.NotAfter.Add(-30*24*time.Hour)) {
		return false
	}
	return c.VerifyHostname(ip) == nil
}

// newCert makes a CA (caPEM nil) or a server certificate signed by it.
func newCert(caPEM, caKeyPEM []byte, cn string, hosts []string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	parent, signer := tmpl, any(key)
	if caPEM == nil {
		tmpl.IsCA, tmpl.BasicConstraintsValid = true, true
		tmpl.KeyUsage |= x509.KeyUsageCertSign
		tmpl.NotAfter = time.Now().AddDate(10, 0, 0)
	} else {
		cb, _ := pem.Decode(caPEM)
		kb, _ := pem.Decode(caKeyPEM)
		if cb == nil || kb == nil {
			return nil, nil, errors.New("the local CA files are unreadable")
		}
		ca, err := x509.ParseCertificate(cb.Bytes)
		if err != nil {
			return nil, nil, err
		}
		caKey, err := x509.ParseECPrivateKey(kb.Bytes)
		if err != nil {
			return nil, nil, err
		}
		parent, signer = ca, caKey
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, h := range hosts {
			if ip := net.ParseIP(h); ip != nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			} else {
				tmpl.DNSNames = append(tmpl.DNSNames, h)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}
