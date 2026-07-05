package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// hyve-server has its own auth layer (forward-auth middleware, applied
	// to this route like every other protected route); origin/CORS policy
	// beyond that is the embedding frontend's concern, not hyve's.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ExecutionsWSHandlers backs WS /executions/:id/stream.
type ExecutionsWSHandlers struct {
	*Deps
}

func NewExecutionsWSHandlers(deps *Deps) *ExecutionsWSHandlers { return &ExecutionsWSHandlers{deps} }

// Stream handles WS /executions/:id/stream. It replays every stored log line
// first so late subscribers catch up, then streams live lines until the
// execution reaches a terminal state, at which point the server closes the
// connection.
func (h *ExecutionsWSHandlers) Stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, ok := h.Registry.Get(id)
	if !ok {
		http.Error(w, fmt.Sprintf("execution %q not found", id), http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // upgrader already wrote an error response
	}
	defer conn.Close()

	replay, ch, unsubscribe := e.Subscribe()
	defer unsubscribe()

	// gorilla/websocket requires a continuous reader to process control
	// frames and detect a client-initiated close; this connection is
	// otherwise server->client only, so every read is discarded except as a
	// signal that the client disconnected.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for _, line := range replay {
		if conn.WriteJSON(line) != nil {
			return
		}
	}

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				// Execution reached a terminal state — close cleanly rather
				// than letting the deferred conn.Close() drop the connection
				// abruptly.
				deadline := time.Now().Add(time.Second)
				conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
				return
			}
			if conn.WriteJSON(line) != nil {
				return
			}
		case <-closed:
			return
		}
	}
}
