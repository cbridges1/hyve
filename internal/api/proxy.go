package api

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// BuildProxy returns a reverse proxy that forwards /proxy/*-prefixed
// requests to target (normally "https://kubernetes.default.svc"), trusting
// caCert for that upstream TLS connection — the API pod's own in-cluster
// CA. The forwarded bearer token (set by the caller, already required by
// requireAuth/requireRole to be a valid hyve session — the token that
// actually authorizes the Kubernetes-side request is the one
// PrimaryClusterProvider minted, present in the client's own kubeconfig
// and sent as this request's own Authorization header when the client is
// `kubectl --kubeconfig <minted-file>`) is passed straight through
// unmodified. This handler does not re-implement authorization — the
// target kube-apiserver's own RBAC is the actual authority; re-checking
// here would just be a second system that can drift from the first.
//
// Mount this behind http.StripPrefix("/proxy", ...) — see Server.Routes —
// so paths match what the real kube-apiserver expects.
func BuildProxy(target string, caCert []byte) (http.Handler, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse proxy target %q: %w", target, err)
	}

	pool := x509.NewCertPool()
	if len(caCert) > 0 {
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("no valid certificates found in provided CA")
		}
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	return proxy, nil
}
