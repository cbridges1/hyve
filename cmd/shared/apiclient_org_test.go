package shared

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
