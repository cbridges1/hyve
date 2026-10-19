package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/rancher/remotedialer"
)

// Header names cattle-cluster-agent sends.
const (
	HeaderToken  = "X-API-Tunnel-Token"
	HeaderParams = "X-API-Tunnel-Params"
)

// Server accepts agents' tunnels and dials through them.
type Server struct {
	Registry *Registry
	// CACerts is the PEM CA the host's certificate chains to, served at
	// /v3/settings/cacerts for agents pinning it with CATTLE_CA_CHECKSUM;
	// empty for a publicly trusted certificate.
	CACerts string

	dialer *remotedialer.Server
}

// NewServer returns a tunnel server.
func NewServer(reg *Registry, caCerts string) *Server {
	// Served without a trailing newline, as Rancher stores its cacerts
	// setting; the agent's checksum assumes that.
	s := &Server{Registry: reg, CACerts: strings.TrimRight(caCerts, "\n")}
	s.dialer = remotedialer.New(s.authorize, remotedialer.DefaultErrorWriter)
	return s
}

// authorize checks an agent's registration token and records the cluster
// details it reports. The remotedialer session is keyed by cluster ID.
func (s *Server) authorize(req *http.Request) (string, bool, error) {
	ctx := req.Context()
	agent, err := s.Registry.ByToken(ctx, req.Header.Get(HeaderToken))
	if errors.Is(err, ErrUnknownToken) {
		log.Printf("tunnel: refused an agent from %s: unknown token", req.RemoteAddr)
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(req.Header.Get(HeaderParams))
	if err != nil {
		return "", false, err
	}
	var p clusterParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", false, err
	}
	if p.Cluster == nil || p.Cluster.Address == "" || p.Cluster.Token == "" || p.Cluster.CACert == "" {
		// Node agents (custom clusters) aren't supported: hyve's clusters
		// come from Cluster API.
		log.Printf("tunnel: refused an agent for %s: no cluster address, token or CA", agent.ClusterID)
		return "", false, nil
	}
	ca, err := decodeCA(p.Cluster.CACert)
	if err != nil {
		return "", false, err
	}
	if err := s.Registry.Report(ctx, agent.ClusterID, p.Cluster.Address, ca, p.Cluster.Token); err != nil {
		return "", false, err
	}
	log.Printf("tunnel: agent connected for %s (API %s)", agent.ClusterID, p.Cluster.Address)
	return agent.ClusterID, true, nil
}

// Connected reports whether a cluster's agent has a tunnel open.
func (s *Server) Connected(id string) bool { return s.dialer.HasSession(id) }

// Dialer dials through a cluster's tunnel.
func (s *Server) Dialer(id string) remotedialer.Dialer { return s.dialer.Dialer(id) }

// Clients lists the clusters with a tunnel open.
func (s *Server) Clients() []string { return s.dialer.ListClients() }

// Register adds the agent-facing routes: /v3/connect[/register] and
// /v3/settings/cacerts. /ping is the sign-in server's.
func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("/v3/connect", s.dialer)
	mux.Handle("/v3/connect/register", s.dialer)
	mux.HandleFunc("GET /v3/settings/cacerts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "cacerts", "value": s.CACerts})
	})
}
