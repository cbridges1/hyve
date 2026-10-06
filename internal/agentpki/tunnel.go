package agentpki

import (
	"fmt"
	"net/url"
	"strings"
)

// TunnelWebSocketPath is hyve-api's WebSocket endpoint for the agent
// tunnel, on its main HTTP listener: the same SSH session the raw TCP
// listener (api.agentBindAddress, :8092) serves, carried over a WebSocket
// so it reaches hyve-api through any HTTPS reverse proxy — the way
// Rancher's cluster agent tunnels over its server URL — instead of needing
// a raw TCP port exposed publicly.
const TunnelWebSocketPath = "/agent/tunnel"

// IsWebSocketTunnelAddress reports whether a tunnel address is a ws:// or
// wss:// URL rather than a raw TCP host:port.
func IsWebSocketTunnelAddress(addr string) bool {
	return strings.HasPrefix(addr, "wss://") || strings.HasPrefix(addr, "ws://")
}

// TunnelWebSocketURL derives the WebSocket tunnel URL from hyve-api's
// control-plane base URL: https://host[/prefix] becomes
// wss://host[/prefix]/agent/tunnel (http becomes ws).
func TunnelWebSocketURL(controlPlaneURL string) (string, error) {
	u, err := url.Parse(controlPlaneURL)
	if err != nil {
		return "", fmt.Errorf("parse control plane URL %q: %w", controlPlaneURL, err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("control plane URL %q must be http:// or https://", controlPlaneURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("control plane URL %q has no host", controlPlaneURL)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + TunnelWebSocketPath
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}
