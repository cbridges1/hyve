package agentpki

import (
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// AgentCertTTL is how long a signed agent user certificate is valid for —
// short enough that a stolen certificate has a bounded exploitation
// window (see "Agent identity / authentication"'s own reasoning), long
// enough that an agent doesn't need to renew constantly. hyve-agent
// itself (milestone 3) is responsible for renewing before this expires,
// over its own already-open connection.
const AgentCertTTL = 24 * time.Hour

// principalSeparator joins namespace and cluster name into one SSH
// certificate principal. '/' is safe and unambiguous here — unlike a
// hyphen-based join (see docs/HYVE-ORGANIZATION-MODEL-PROPOSAL.md's own
// naming-collision reasoning for exactly this kind of ambiguity), neither
// a Kubernetes namespace nor object name can ever contain a literal '/'
// (both are DNS-1123 labels: lowercase alphanumeric and hyphens only), so
// splitting on the first '/' is always unambiguous — no escaping scheme
// needed.
const principalSeparator = "/"

// AgentPrincipal returns the single SSH certificate principal
// SignAgentUserCertificate encodes cluster identity into, and
// ParseAgentPrincipal decodes it back out of — the pair a milestone 3
// listener uses to know which (namespace, clusterName) a connecting
// agent's certificate actually asserts, cryptographically, rather than by
// unauthenticated claim (see "Multi-tenancy scoping of the connection
// registry").
func AgentPrincipal(namespace, clusterName string) string {
	return namespace + principalSeparator + clusterName
}

// ParseAgentPrincipal is AgentPrincipal's inverse.
func ParseAgentPrincipal(principal string) (namespace, clusterName string, err error) {
	for i := 0; i < len(principal); i++ {
		if principal[i] == '/' {
			return principal[:i], principal[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("malformed agent principal %q: no %q separator", principal, principalSeparator)
}

// SignAgentUserCertificate signs agentPublicKey (submitted by the agent in
// OpenSSH authorized_keys format — there's no X.509-style CSR object in
// SSH, the agent just submits its raw public key) into a short-lived user
// certificate whose one ValidPrincipal is AgentPrincipal(namespace,
// clusterName) — the cryptographic binding "Multi-tenancy scoping of the
// connection registry" relies on to tie agent identity to tenant identity,
// rather than trusting an unauthenticated claim at connect time.
func (c *CA) SignAgentUserCertificate(agentPublicKeyAuthorized []byte, namespace, clusterName string) (*ssh.Certificate, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(agentPublicKeyAuthorized)
	if err != nil {
		return nil, fmt.Errorf("parse agent public key: %w", err)
	}
	principal := AgentPrincipal(namespace, clusterName)
	return c.signCertificate(pub, ssh.UserCert, []string{principal}, principal, AgentCertTTL)
}
