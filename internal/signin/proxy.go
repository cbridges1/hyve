package signin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"
)

// localPrefix is stripped before a request reaches the API server.
const localPrefix = "/k8s/clusters/local"

// SessionLookup resolves a hyve session token.
type SessionLookup interface {
	Lookup(ctx context.Context, token string) (Identity, error)
}

// LocalProxy serves /k8s/clusters/local: the management cluster's API.
//
// A request carrying a hyve session (a bearer token, or the UI's cookie)
// is checked, stripped of its own
// credentials and any Impersonate-* headers, and forwarded with the host's
// service account impersonating "hyve:<username>" and the user's groups,
// so management-cluster RBAC decides it. Any other request, such as
// fleet-agent's service-account token or an admin's own kubeconfig
// credentials, is forwarded untouched over TLS, and the API server
// authenticates it itself.
//
// Kubernetes can't limit impersonation to "hyve:" names, so the host's
// service account can impersonate anyone; this is the only place it does,
// and only with names built from a valid session.
type LocalProxy struct {
	sessions    SessionLookup
	impersonate *httputil.ReverseProxy
	passthrough *httputil.ReverseProxy
}

// NewLocalProxy builds the proxy from the host's own rest.Config, usually
// the in-cluster config.
func NewLocalProxy(cfg *rest.Config, sessions SessionLookup) (*LocalProxy, error) {
	target, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" {
		target.Scheme = "https"
	}

	// HTTP/1.1 only: exec, attach, port-forward and watch upgrade the
	// connection (SPDY or WebSocket), which HTTP/2 can't carry.
	saCfg := rest.CopyConfig(cfg)
	saCfg.NextProtos = []string{"http/1.1"}
	saTransport, err := rest.TransportFor(saCfg)
	if err != nil {
		return nil, err
	}

	anonCfg := rest.AnonymousClientConfig(cfg)
	anonCfg.NextProtos = []string{"http/1.1"}
	anonTransport, err := rest.TransportFor(anonCfg)
	if err != nil {
		return nil, err
	}

	return &LocalProxy{
		sessions:    sessions,
		impersonate: newReverseProxy(target, saTransport),
		passthrough: newReverseProxy(target, anonTransport),
	}, nil
}

func newReverseProxy(target *url.URL, transport http.RoundTripper) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			path := strings.TrimPrefix(pr.In.URL.Path, localPrefix)
			if path == "" {
				path = "/"
			}
			pr.Out.URL.Path = singleJoin(target.Path, path)
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
		},
		Transport:     transport,
		FlushInterval: -1, // stream watches and logs as they arrive
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("signin: proxying %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadGateway, "BadGateway", "the management cluster's API didn't respond")
		},
	}
}

func singleJoin(a, b string) string {
	return strings.TrimSuffix(a, "/") + "/" + strings.TrimPrefix(b, "/")
}

func (p *LocalProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := sessionToken(r)
	if !ok {
		writeError(w, http.StatusForbidden, "Forbidden", "missing the "+UIHeader+" header")
		return
	}
	if !IsSessionToken(token) {
		if _, err := r.Cookie(SessionCookie); err == nil {
			r = r.Clone(r.Context())
			r.Header.Del("Cookie")
		}
		p.passthrough.ServeHTTP(w, r)
		return
	}

	ident, err := p.sessions.Lookup(r.Context(), token)
	if err != nil {
		if !errors.Is(err, ErrInvalidSession) {
			log.Printf("signin: session lookup: %v", err)
		}
		writeError(w, http.StatusUnauthorized, "Unauthorized", ErrInvalidSession.Error())
		return
	}

	r = r.Clone(r.Context())
	r.Header.Del("Authorization")
	r.Header.Del("Cookie")
	r.Header.Del(UIHeader)
	for h := range r.Header {
		if strings.HasPrefix(strings.ToLower(h), "impersonate-") {
			r.Header.Del(h)
		}
	}
	r.Header.Set("Impersonate-User", ident.KubeUser())
	for _, g := range ident.KubeGroups() {
		r.Header.Add("Impersonate-Group", g)
	}
	p.impersonate.ServeHTTP(w, r)
}
