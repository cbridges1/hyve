package audit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	r := httptest.NewRequest(http.MethodGet, "/k8s/clusters/dev.web/api/v1/pods?watch=true", nil)
	rec := httptest.NewRecorder()
	w, done := Start(rec, r, "hyve:alice", []string{"hyve:ops", "system:authenticated"}, "dev.web")
	w.WriteHeader(http.StatusForbidden)
	done()

	var e Entry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &e))
	assert.True(t, e.Audit)
	assert.Equal(t, "hyve:alice", e.User)
	assert.Equal(t, "dev.web", e.Cluster)
	assert.Equal(t, "watch", e.Verb)
	assert.Equal(t, "/k8s/clusters/dev.web/api/v1/pods", e.Path)
	assert.Equal(t, http.StatusForbidden, e.Status)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// http.ResponseController reaches the real writer.
	assert.NoError(t, http.NewResponseController(w).Flush())

	Enabled = false
	defer func() { Enabled = true }()
	buf.Reset()
	_, done = Start(rec, r, "hyve:alice", nil, "local")
	done()
	assert.Empty(t, buf.String())
}
