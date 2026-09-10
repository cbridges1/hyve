package agentpki

// HeartbeatRequestType is the one global SSH request type an agent sends
// over its already-open tunnel connection as a lightweight liveness/
// version signal — folded into the existing session rather than a
// separate polling mechanism (see the proposal doc's own "Agent
// responsibilities"). Shared here, in the one package both
// internal/agent (the sender) and internal/api (the receiver) already
// depend on for identity/signing, rather than duplicated as a
// keep-these-two-strings-in-sync-by-hand constant in each.
const HeartbeatRequestType = "heartbeat@hyve.io"

// HeartbeatPayload is HeartbeatRequestType's JSON payload.
type HeartbeatPayload struct {
	Version string `json:"version"`

	// ServiceAccountToken is the agent's own current in-cluster
	// ServiceAccount token (freshly read off its mounted projected-token
	// file on every heartbeat, never cached — see internal/agent/
	// heartbeat.go, since kubelet rotates that file's contents in place
	// roughly hourly and a stale copy would eventually 401 against the
	// agent's own local apiserver). This is milestone 5's proxy path's
	// only source for a token that can actually authenticate to the
	// managed cluster's apiserver: hyve-api's own reverse proxy
	// (internal/api/agent_proxy.go) is what makes the real outbound HTTP
	// call over the tunnel, so it needs a real bearer token in hand, not
	// just a raw byte-relay — see that file's own doc comment for the
	// full Impersonate-User/-Group flow this token, plus impersonation
	// headers, together enable.
	ServiceAccountToken string `json:"serviceAccountToken,omitempty"`
}
