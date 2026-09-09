package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/cbridges1/hyve/internal/agentpki"

	"golang.org/x/crypto/ssh"
)

// maxAgentBootstrapBodyBytes bounds the bootstrap request body — plenty
// for a token plus one SSH public key line, and a hard ceiling against a
// runaway or malicious POST to an endpoint that, deliberately, has no
// hyve session/role gate in front of it.
const maxAgentBootstrapBodyBytes = 1 << 16 // 64KiB

type agentBootstrapRequest struct {
	Token     string `json:"token"`
	PublicKey string `json:"publicKey"`
}

type agentBootstrapResponse struct {
	Certificate string `json:"certificate"`
	// CAPublicKey is the same CA's own public key, in authorized_keys
	// format — what the agent needs to verify hyve-api's own host
	// certificate on every future connection (ssh.CertChecker.
	// IsHostAuthority). Returned here rather than needing a second
	// endpoint or a separately-distributed value: this response is
	// already the one place trust gets established (the bootstrap token
	// was the proof), so folding the CA's public key into it doesn't
	// weaken anything a second round trip would have protected — an
	// attacker able to forge this response could already return a fake
	// certificate too.
	CAPublicKey string `json:"caPublicKey"`
}

// registerAgentBootstrapRoutes wires POST /agent/bootstrap — mounted on
// Server.Routes' own top-level mux (see server.go), deliberately alongside
// /auth/login rather than under /api/: this has no hyve session concept
// of its own (requireAuth/requireRole don't apply — an agent dialing in
// from a genuinely external, remote cluster has no hyve session to
// present), the single-use bootstrap token itself is the entire
// authorization model, exactly like handleAccessMethodMintRelay's own
// per-request bearer token — except this endpoint has to be reachable
// from outside this API's own cluster (an agent runs on the managed
// cluster being bootstrapped, not this one), unlike that relay listener,
// which is deliberately never exposed past this cluster's own pod
// network. See docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Agent identity
// / authentication".
func (s *Server) registerAgentBootstrapRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /agent/bootstrap", s.handleAgentBootstrap)
}

// handleAgentBootstrap validates a single-use bootstrap token (minted by
// the reconcile loop when installing an agent — milestone 4) and, if
// valid, signs the agent's submitted public key into a short-lived user
// certificate scoped to whichever (namespace, clusterName) the token was
// generated for — never a value the request itself supplies, so a caller
// can't request a certificate for a cluster its token wasn't actually
// minted for.
func (s *Server) handleAgentBootstrap(w http.ResponseWriter, r *http.Request) {
	if s.AgentCA == nil {
		writeError(w, http.StatusInternalServerError, "agent bootstrap is not configured on this API (no agent CA)")
		return
	}

	var req agentBootstrapRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentBootstrapBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" || req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "token and publicKey are required")
		return
	}

	namespace, clusterName, err := agentpki.ValidateAndConsumeBootstrapToken(r.Context(), s.Clientset, s.Namespace, req.Token)
	if err != nil {
		// Deliberately the same 401 + generic message whether the token
		// was never issued, already used, or expired — no distinction
		// that would let a caller learn something from the difference,
		// same stance the token validation itself already takes.
		writeError(w, http.StatusUnauthorized, "invalid or already-used bootstrap token")
		return
	}

	cert, err := s.AgentCA.SignAgentUserCertificate([]byte(req.PublicKey), namespace, clusterName)
	if err != nil {
		log.Printf("api: failed to sign agent certificate for %s/%s: %v", namespace, clusterName, err)
		writeError(w, http.StatusBadRequest, "invalid public key")
		return
	}

	writeJSON(w, http.StatusOK, agentBootstrapResponse{
		Certificate: string(ssh.MarshalAuthorizedKey(cert)),
		CAPublicKey: string(ssh.MarshalAuthorizedKey(s.AgentCA.PublicKey())),
	})
}
