package tunnel

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	"k8s.io/client-go/rest"
)

// RESTConfig reaches child cluster id as ident, through this proxy in the
// same process: the host's own Kubernetes clients (the UI's watch stream)
// go the way a user's kubectl would, tunnel or direct path.
func (p *Proxy) RESTConfig(id string, ident Identity) *rest.Config {
	return &rest.Config{
		Host:      "http://hyve-host" + clustersPrefix + id,
		Transport: &inProcess{p: p, ident: ident},
	}
}

// inProcess serves requests with the proxy's handler, streaming the
// response, so watches work.
type inProcess struct {
	p     *Proxy
	ident Identity
	// serve replaces the proxy (tests).
	serve func(http.ResponseWriter, *http.Request)
}

func (t *inProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	w := &pipeWriter{header: http.Header{}, pw: pw, ready: make(chan struct{})}
	go func() {
		defer func() {
			// The reverse proxy aborts with a panic when the reader goes
			// away mid-stream (a watch closed); net/http's server recovers
			// that, so do the same here rather than crash the host.
			if v := recover(); v != nil {
				if v != http.ErrAbortHandler {
					log.Printf("tunnel: in-process request to %s panicked: %v", req.URL.Path, v)
				}
				w.WriteHeader(http.StatusBadGateway)
				_ = pw.CloseWithError(fmt.Errorf("%v", v))
			}
		}()
		if t.serve != nil {
			t.serve(w, req)
		} else {
			t.p.serveAs(w, req, t.ident)
		}
		w.WriteHeader(http.StatusOK)
		_ = pw.Close()
	}()
	select {
	case <-w.ready:
	case <-req.Context().Done():
		_ = pr.CloseWithError(req.Context().Err())
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: w.status,
		Status:     http.StatusText(w.status),
		Header:     w.sent,
		Body:       pr,
		Request:    req,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}, nil
}

type pipeWriter struct {
	header http.Header
	pw     *io.PipeWriter
	status int
	sent   http.Header
	once   sync.Once
	ready  chan struct{}
}

func (w *pipeWriter) Header() http.Header { return w.header }

func (w *pipeWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		w.sent = w.header.Clone()
		close(w.ready)
	})
}

func (w *pipeWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

// Flush is a no-op: every Write already reaches the reader.
func (w *pipeWriter) Flush() {}
