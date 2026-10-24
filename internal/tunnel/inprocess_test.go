package tunnel

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A handler that aborts (as the reverse proxy does when its reader goes
// away) must not take the process down.
func TestInProcessRecoversAbort(t *testing.T) {
	rt := &inProcess{}
	rt.serve = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
		panic(http.ErrAbortHandler)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://hyve-host/k8s/clusters/a/api", nil)
	resp, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	assert.Error(t, err)
}
