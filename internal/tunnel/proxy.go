package tunnel

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Identity is the signed-in user a request runs as on the child cluster.
type Identity interface {
	KubeUser() string
	KubeGroups() []string
}

// Authenticate resolves a request's session; ok false means it has none.
type Authenticate func(r *http.Request) (Identity, bool)

// Proxy serves /k8s/clusters/<id>/... for child clusters: it checks the
// caller's session, then forwards the request through the cluster's agent
// tunnel to its API server, with the agent's "cattle" service account
// impersonating the caller (hyve:<username> and hyve:<group>s), so the
// child's RBAC decides it, as with Rancher's proxy. exec, attach,
// port-forward and watch work: the connection is upgraded end to end.
type Proxy struct {
	Server       *Server
	Authenticate Authenticate

	mu         sync.Mutex
	transports map[string]cachedTransport
}

type cachedTransport struct {
	key string
	rt  http.RoundTripper
}

const clustersPrefix = "/k8s/clusters/"

func writeStatus(w http.ResponseWriter, code int, reason, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":%q,"reason":%q,"code":%d}`, msg, reason, code)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, clustersPrefix)
	id, path, _ := strings.Cut(rest, "/")
	ident, ok := p.Authenticate(r)
	if !ok {
		writeStatus(w, http.StatusUnauthorized, "Unauthorized", "sign in with 'hyve login' first")
		return
	}
	agent, err := p.Server.Registry.Get(r.Context(), id)
	switch {
	case apierrors.IsNotFound(err):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("cluster %q isn't known to this hyve host", id))
		return
	case err != nil:
		log.Printf("tunnel: looking up %s: %v", id, err)
		writeStatus(w, http.StatusInternalServerError, "InternalError", "looking up the cluster failed")
		return
	case agent.Address == "" || !p.Server.Connected(id):
		writeStatus(w, http.StatusServiceUnavailable, "ServiceUnavailable", fmt.Sprintf("cluster %s's agent isn't connected", id))
		return
	}
	rt, err := p.transport(agent)
	if err != nil {
		log.Printf("tunnel: transport for %s: %v", id, err)
		writeStatus(w, http.StatusInternalServerError, "InternalError", "the cluster's CA certificate is invalid")
		return
	}
	target := &url.URL{Scheme: "https", Host: agent.Address}
	out := r.Clone(r.Context())
	out.Header.Del("Authorization")
	out.Header.Del("Cookie")
	for h := range out.Header {
		if strings.HasPrefix(strings.ToLower(h), "impersonate-") {
			out.Header.Del(h)
		}
	}
	out.Header.Set("Authorization", "Bearer "+agent.ServiceAccountToken)
	out.Header.Set("Impersonate-User", ident.KubeUser())
	for _, g := range ident.KubeGroups() {
		out.Header.Add("Impersonate-Group", g)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/" + path
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
		},
		Transport:     rt,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("tunnel: proxying %s %s to %s: %v", r.Method, r.URL.Path, id, err)
			writeStatus(w, http.StatusBadGateway, "BadGateway", fmt.Sprintf("cluster %s's API didn't respond through its agent", id))
		},
	}
	rp.ServeHTTP(w, out)
}

// transport returns an HTTP/1.1 transport that dials through the cluster's
// tunnel and trusts its CA, rebuilt when the agent reports new details.
func (p *Proxy) transport(a *Agent) (http.RoundTripper, error) {
	sum := sha256.Sum256(append([]byte(a.Address+"\x00"), a.CACert...))
	key := string(sum[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.transports == nil {
		p.transports = map[string]cachedTransport{}
	}
	if c, ok := p.transports[a.ClusterID]; ok && c.key == key {
		return c.rt, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(a.CACert) {
		return nil, errors.New("no certificates in the agent's CA")
	}
	dial := p.Server.Dialer(a.ClusterID)
	rt := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		// Upgrades (exec, port-forward) need HTTP/1.1.
		ForceAttemptHTTP2:     false,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 0,
		MaxIdleConnsPerHost:   20,
	}
	p.transports[a.ClusterID] = cachedTransport{key: key, rt: rt}
	return rt, nil
}
