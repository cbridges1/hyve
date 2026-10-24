// Package audit writes the host's audit log (HYVE-PROPOSAL.md, plan step
// 5): one structured line per request the host proxies to a cluster as a
// user (user, groups, cluster, verb, path, status, duration). Each child's
// own API-server audit policy stays its provider's concern.
package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	mu  sync.Mutex
	out io.Writer = os.Stdout
	// Enabled turns the log on (the host's --audit-log).
	Enabled = true
)

// SetOutput sends the log to w (tests).
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	out = w
}

// Entry is one line of the log.
type Entry struct {
	Time       string   `json:"time"`
	Audit      bool     `json:"audit"`
	User       string   `json:"user"`
	Groups     []string `json:"groups,omitempty"`
	Cluster    string   `json:"cluster"`
	Verb       string   `json:"verb"`
	Path       string   `json:"path"`
	Query      string   `json:"query,omitempty"`
	Status     int      `json:"status"`
	DurationMS int64    `json:"durationMs"`
	Client     string   `json:"client,omitempty"`
	UserAgent  string   `json:"userAgent,omitempty"`
}

// Start begins a request's entry: serve through the returned writer, then
// call done.
func Start(w http.ResponseWriter, r *http.Request, user string, groups []string, cluster string) (http.ResponseWriter, func()) {
	if !Enabled {
		return w, func() {}
	}
	rec := &recorder{ResponseWriter: w}
	start := time.Now()
	return rec, func() {
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		e := Entry{
			Time: start.UTC().Format(time.RFC3339Nano), Audit: true,
			User: user, Groups: groups, Cluster: cluster,
			Verb: verb(r), Path: r.URL.Path, Query: r.URL.RawQuery,
			Status: status, DurationMS: time.Since(start).Milliseconds(),
			Client: clientIP(r), UserAgent: r.UserAgent(),
		}
		b, err := json.Marshal(e)
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if _, err := out.Write(append(b, '\n')); err != nil {
			log.Printf("audit: %v", err)
		}
	}
}

// verb is the request's Kubernetes verb, roughly: watch and list are told
// apart from get by their query.
func verb(r *http.Request) string {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if q.Get("watch") == "true" || q.Get("watch") == "1" {
			return "watch"
		}
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	}
	return r.Method
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return f
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// recorder notes the status; Unwrap lets http.ResponseController (and so
// the reverse proxy's flushing and upgrades) reach the real writer.
type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack is for upgrades (exec, port-forward): 101 Switching Protocols.
func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("audit: the response can't be hijacked")
	}
	if r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}
