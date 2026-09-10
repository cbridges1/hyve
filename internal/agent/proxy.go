package agent

import (
	"io"
	"log"
	"net"

	"github.com/cbridges1/hyve/internal/agentpki"

	"golang.org/x/crypto/ssh"
)

// serveProxyChannels services every ProxyChannelType channel hyve-api
// opens against client for as long as client stays connected — see
// connectOnce's own call site. (*ssh.Client).HandleChannelOpen returns a
// channel that's closed once the underlying connection is; this loop
// exits naturally on disconnect, needing no separate ctx of its own.
func serveProxyChannels(client *ssh.Client) {
	incoming := client.HandleChannelOpen(agentpki.ProxyChannelType)
	for newChannel := range incoming {
		go handleProxyChannel(newChannel)
	}
}

// handleProxyChannel accepts one proxy channel and relays raw bytes
// between it and a fresh local TCP connection to
// agentpki.KubernetesAPIServerAddr — a pure L4 byte-forward, deliberately:
// this process never terminates TLS or parses HTTP for a proxied request
// at all. hyve-api's own reverse proxy (internal/api/agent_proxy.go) is
// what does the real TLS handshake and HTTP round trip, treating this
// channel as nothing more than the raw transport underneath — see that
// file's own doc comment for why (it's the only side that ever holds a
// bearer token/impersonation headers for the request, so it has to be the
// one actually speaking HTTP).
func handleProxyChannel(newChannel ssh.NewChannel) {
	channel, requests, err := newChannel.Accept()
	if err != nil {
		log.Printf("agent: failed to accept proxy channel: %v", err)
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)

	local, err := net.Dial("tcp", agentpki.KubernetesAPIServerAddr)
	if err != nil {
		log.Printf("agent: proxy channel: failed to dial local apiserver %s: %v", agentpki.KubernetesAPIServerAddr, err)
		return
	}
	defer local.Close()

	relay(channel, local)
}

// halfCloser is satisfied by both ssh.Channel and *net.TCPConn (what
// net.Dial("tcp", ...) actually returns, behind the net.Conn interface) —
// checked via a type assertion below since neither io.ReadWriteCloser nor
// net.Conn expose CloseWrite on their own. Half-closing the write side
// once one direction's copy finishes (rather than only ever fully closing
// both ends together, after both directions finish) matters for a
// protocol that itself relies on seeing EOF-on-write independent of the
// read side, which SPDY (kubectl exec/attach) does.
type halfCloser interface {
	CloseWrite() error
}

// relay copies bytes in both directions between a and b until each
// direction's own copy finishes (a real remote close, not just one
// direction going idle), half-closing whichever side finished writing so
// the other side observes that as a clean EOF rather than staying open
// indefinitely.
func relay(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	copyHalf := func(dst, src io.ReadWriteCloser) {
		_, _ = io.Copy(dst, src)
		if hc, ok := dst.(halfCloser); ok {
			_ = hc.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	<-done
	<-done
}
