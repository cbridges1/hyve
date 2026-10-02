package agentpki

import "golang.org/x/crypto/ssh"

// ProxyChannelType is the SSH channel type hyve-api opens (as the SSH
// server) against a connected agent (the SSH client) to proxy one
// Kubernetes API request — see docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's
// "Proxy authorization model". Named "forwarded-tcpip" per RFC 4254 §7.2's
// own wire format (ProxyChannelPayload below matches that message's field
// layout exactly), even though hyve never uses OpenSSH's own -R/tcpip-
// forward convention that name is normally associated with — nothing in
// the SSH protocol itself restricts which side may open a channel of a
// given type; server-initiated "please dial this for me" is exactly what
// this channel type's payload shape already expresses, and reusing it
// avoids inventing a new wire format for the same four fields. The
// agent's own internal/agent/proxy.go registers a handler for exactly
// this one type via (*ssh.Client).HandleChannelOpen.
const ProxyChannelType = "forwarded-tcpip"

// KubernetesAPIServerAddr is the one dial target every proxy channel is
// for — the agent's own local in-cluster apiserver, always reachable at
// this well-known in-cluster DNS name from any pod, exactly the same
// address any other in-cluster client (e.g. an in-cluster kubeconfig)
// would use. Never any other host: hyve-agent has no general-purpose
// "dial anywhere" story, deliberately — the proxy exists to reach one
// specific, known-safe destination, not to turn the agent into a generic
// SOCKS relay.
const KubernetesAPIServerAddr = "kubernetes.default.svc:443"

// ProxyChannelPayload is ProxyChannelType's channel-open payload, RFC
// 4254 §7.2's wire format for both "direct-tcpip" and "forwarded-tcpip"
// (the two share an identical payload shape) — connected-address/port is
// what the receiving side should dial; originator-address/port identifies
// the logical requester, informational only here (there's no real TCP
// peer behind hyve-api's own OpenChannel call the way there would be for
// an actual forwarded listener), never inspected by internal/agent's own
// handler.
type ProxyChannelPayload struct {
	ConnectedAddress  string
	ConnectedPort     uint32
	OriginatorAddress string
	OriginatorPort    uint32
}

// NewProxyChannelPayload returns the marshaled ProxyChannelPayload for
// KubernetesAPIServerAddr — the one payload every OpenChannel(ProxyChannelType, ...)
// call needs; a function rather than a package-level []byte since
// ssh.Marshal's result must not be shared/mutated across calls.
func NewProxyChannelPayload() []byte {
	return ssh.Marshal(&ProxyChannelPayload{
		ConnectedAddress:  "kubernetes.default.svc",
		ConnectedPort:     443,
		OriginatorAddress: "hyve-api",
	})
}

// AgentProxyAdminGroup/AgentProxyReadOnlyGroup are the Impersonate-Group
// values hyve-api's proxy handler (internal/api/agent_proxy.go) sends,
// and the exact two Kubernetes Group names the agent-install reconcile
// step (internal/reconcile/agent.go) binds to cluster-admin/view via
// ClusterRoleBinding when spec.access.agent.proxy is true — see
// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Proxy authorization model":
// "the exact same two RoleBindings... admin->cluster-admin,
// read-only->view". Defined once, here, in the one package both of those
// already depend on for agent identity/protocol constants, rather than
// duplicated between them with a comment asking the reader to keep two
// string literals in sync by hand.
const (
	AgentProxyAdminGroup    = "hyve:admin"
	AgentProxyReadOnlyGroup = "hyve:read-only"
)
