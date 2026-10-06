package agentpki

import "testing"

func TestTunnelWebSocketURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://hyve.hosted.matlass.com":  "wss://hyve.hosted.matlass.com/agent/tunnel",
		"https://hyve.example.com/":        "wss://hyve.example.com/agent/tunnel",
		"http://hyve-api.127.0.0.1.nip.io": "ws://hyve-api.127.0.0.1.nip.io/agent/tunnel",
		"https://example.com/hyve/?x=1":    "wss://example.com/hyve/agent/tunnel",
		"https://hyve.example.com:8443":    "wss://hyve.example.com:8443/agent/tunnel",
	} {
		got, err := TunnelWebSocketURL(in)
		if err != nil || got != want {
			t.Errorf("TunnelWebSocketURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"hyve.example.com", "ftp://x", "https://"} {
		if _, err := TunnelWebSocketURL(bad); err == nil {
			t.Errorf("TunnelWebSocketURL(%q): want error", bad)
		}
	}
	if !IsWebSocketTunnelAddress("wss://h/agent/tunnel") || !IsWebSocketTunnelAddress("ws://h") || IsWebSocketTunnelAddress("h:8092") {
		t.Error("IsWebSocketTunnelAddress misclassified an address")
	}
}
