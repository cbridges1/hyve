package shared

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIClient_SendsSelectedOrganization(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(organizationHeader))
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"username":"alice","organizations":[]}`))
	}))
	defer ts.Close()

	_, err := (&APIClient{BaseURL: ts.URL, Token: "tok", Org: "acme"}).Whoami()
	require.NoError(t, err)
	_, err = (&APIClient{BaseURL: ts.URL, Token: "tok"}).Whoami()
	require.NoError(t, err)
	assert.Equal(t, []string{"acme", ""}, got, "the header only when an organization is selected")
}

func TestFormatExpiry(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	assert.Contains(t, FormatExpiry("2026-10-03T15:20:00Z", now), "(in 3h20m)")
	assert.Contains(t, FormatExpiry("2026-10-05T13:00:00Z", now), "(in 2d1h)")
	assert.Contains(t, FormatExpiry("2026-10-03T12:04:30Z", now), "(in 5m)")
	assert.Contains(t, FormatExpiry("2026-10-03T11:00:00Z", now), "overdue")
	assert.Equal(t, "soon", FormatExpiry("soon", now))
}
