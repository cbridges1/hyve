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
}
