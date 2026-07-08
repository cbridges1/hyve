package execution

import (
	"strings"
	"sync"
	"time"
)

// LogLine is one line of captured output, numbered for replay/resume.
type LogLine struct {
	Seq        int       `json:"seq"`
	Line       string    `json:"line"`
	CapturedAt time.Time `json:"capturedAt"`
}

// subscriberBuffer is how many not-yet-delivered lines a slow WebSocket
// subscriber can lag behind before new lines are dropped for it. Replay on
// (re)connect covers catch-up, so dropping is an acceptable trade-off for a
// live tail rather than blocking the execution on a slow reader.
const subscriberBuffer = 256

// Execution tracks one long-running server-triggered operation (a workflow
// run, a reconcile, a module/workflow install). It implements io.Writer so it
// can be plugged directly into workflow.Executor.Output / reconcile.Reconciler.Logger —
// every write is split into lines, appended to the stored log, and fanned out
// to any live WebSocket subscribers.
type Execution struct {
	ID        string     `json:"id"`
	Kind      Kind       `json:"kind"`
	Status    Status     `json:"status"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Error     string     `json:"error,omitempty"`

	mu      sync.RWMutex
	lines   []LogLine
	partial string // buffered bytes not yet terminated by a newline
	nextSeq int
	subs    map[int]chan LogLine
	nextSub int
}

// Write implements io.Writer. Partial (newline-less) writes are buffered
// until a terminating '\n' arrives.
func (e *Execution) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.partial += string(p)
	for {
		idx := strings.IndexByte(e.partial, '\n')
		if idx < 0 {
			break
		}
		e.appendLineLocked(e.partial[:idx])
		e.partial = e.partial[idx+1:]
	}
	return len(p), nil
}

func (e *Execution) appendLineLocked(text string) {
	e.nextSeq++
	line := LogLine{Seq: e.nextSeq, Line: text, CapturedAt: time.Now()}
	e.lines = append(e.lines, line)
	for _, ch := range e.subs {
		select {
		case ch <- line:
		default:
			// Slow subscriber — drop rather than block the execution.
			// Replay-on-subscribe means a late/reconnecting client still
			// catches up on everything stored.
		}
	}
}

// Lines returns a snapshot of every log line captured so far.
func (e *Execution) Lines() []LogLine {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]LogLine, len(e.lines))
	copy(out, e.lines)
	return out
}

// Finish marks the execution as terminal (succeeded or failed depending on
// err), flushes any trailing partial line, and closes every live subscriber
// channel so their stream handlers can end the connection.
func (e *Execution) Finish(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.partial != "" {
		e.appendLineLocked(e.partial)
		e.partial = ""
	}

	now := time.Now()
	e.EndedAt = &now
	if err != nil {
		e.Status = StatusFailed
		e.Error = err.Error()
	} else {
		e.Status = StatusSucceeded
	}
	for _, ch := range e.subs {
		close(ch)
	}
	e.subs = map[int]chan LogLine{}
}

// Subscribe registers a live subscriber and returns a replay of every line
// captured so far plus a channel that receives new lines as they arrive. The
// channel is closed when the execution reaches a terminal state. Call the
// returned unsubscribe func when the caller (e.g. a closed WebSocket) is done
// — it is always safe to call, including after Finish already closed
// everything.
func (e *Execution) Subscribe() (replay []LogLine, ch <-chan LogLine, unsubscribe func()) {
	e.mu.Lock()
	defer e.mu.Unlock()

	replay = make([]LogLine, len(e.lines))
	copy(replay, e.lines)

	if e.Status != StatusRunning {
		// Already finished — nothing live to subscribe to, return a closed channel.
		closed := make(chan LogLine)
		close(closed)
		return replay, closed, func() {}
	}

	id := e.nextSub
	e.nextSub++
	subCh := make(chan LogLine, subscriberBuffer)
	e.subs[id] = subCh

	return replay, subCh, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if existing, ok := e.subs[id]; ok {
			delete(e.subs, id)
			close(existing)
		}
	}
}
