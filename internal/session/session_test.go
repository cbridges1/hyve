package session

import (
	"testing"
	"time"

	"github.com/cbridges1/hyve/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB points the package-singleton database (which Save/Load/Clear
// all go through via database.GetDB()) at a fresh temp dir for this test.
// SetConfigDir only takes effect before GetDB's first call in the process
// — ResetSingleton first undoes any earlier test's (or the CLI's own)
// already-fired sync.Once, so this reliably gets its own isolated database
// regardless of test execution order.
func newTestDB(t *testing.T) {
	t.Helper()
	database.ResetSingleton()
	database.SetConfigDir(t.TempDir())
	t.Cleanup(database.ResetSingleton)
}

func TestLoad_NoneReturnsNilNotError(t *testing.T) {
	newTestDB(t)
	sess, err := Load()
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestSaveThenLoad_RoundTrips(t *testing.T) {
	newTestDB(t)
	want := &Session{
		Username:             "cedric",
		APIURL:               "http://hyve-api.example.com",
		SessionID:            "session-abc123",
		SessionSecret:        "the-raw-secret",
		SessionExpiresAt:     time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		AccessToken:          "the-access-token",
		AccessTokenExpiresAt: time.Now().Add(30 * time.Minute).Format(time.RFC3339),
	}
	require.NoError(t, Save(want))

	got, err := Load()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.Username, got.Username)
	assert.Equal(t, want.APIURL, got.APIURL)
	assert.Equal(t, want.SessionID, got.SessionID)
	assert.Equal(t, want.SessionSecret, got.SessionSecret)
	assert.Equal(t, "session-abc123.the-raw-secret", got.SessionToken())
}

func TestSave_ReplacesPreviousSession(t *testing.T) {
	newTestDB(t)
	require.NoError(t, Save(&Session{Username: "first", APIURL: "a", SessionID: "s1", SessionSecret: "x", SessionExpiresAt: time.Now().Format(time.RFC3339), AccessToken: "t1", AccessTokenExpiresAt: time.Now().Format(time.RFC3339)}))
	require.NoError(t, Save(&Session{Username: "second", APIURL: "b", SessionID: "s2", SessionSecret: "y", SessionExpiresAt: time.Now().Format(time.RFC3339), AccessToken: "t2", AccessTokenExpiresAt: time.Now().Format(time.RFC3339)}))

	got, err := Load()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "second", got.Username)
	assert.Equal(t, "s2", got.SessionID)
}

func TestSaveAccessToken_UpdatesOnlyAccessTokenHalf(t *testing.T) {
	newTestDB(t)
	require.NoError(t, Save(&Session{
		Username: "cedric", APIURL: "a", SessionID: "s1", SessionSecret: "x",
		SessionExpiresAt: "2099-01-01T00:00:00Z", AccessToken: "old-token", AccessTokenExpiresAt: "2000-01-01T00:00:00Z",
	}))

	require.NoError(t, SaveAccessToken("new-token", "2099-06-01T00:00:00Z"))

	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "new-token", got.AccessToken)
	assert.Equal(t, "2099-06-01T00:00:00Z", got.AccessTokenExpiresAt)
	assert.Equal(t, "s1", got.SessionID, "refreshing the access token must not touch the session id/secret")
	assert.Equal(t, "x", got.SessionSecret)
}

func TestSaveAccessToken_NoSessionIsAnError(t *testing.T) {
	newTestDB(t)
	err := SaveAccessToken("token", "2099-01-01T00:00:00Z")
	assert.Error(t, err)
}

func TestClear_RemovesSession(t *testing.T) {
	newTestDB(t)
	require.NoError(t, Save(&Session{Username: "cedric", APIURL: "a", SessionID: "s1", SessionSecret: "x", SessionExpiresAt: "2099-01-01T00:00:00Z", AccessToken: "t", AccessTokenExpiresAt: "2099-01-01T00:00:00Z"}))
	require.NoError(t, Clear())

	got, err := Load()
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestAccessTokenValid(t *testing.T) {
	future := &Session{AccessTokenExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	past := &Session{AccessTokenExpiresAt: time.Now().Add(-time.Hour).Format(time.RFC3339)}
	malformed := &Session{AccessTokenExpiresAt: "not-a-time"}

	assert.True(t, future.AccessTokenValid())
	assert.False(t, past.AccessTokenValid())
	assert.False(t, malformed.AccessTokenValid())
	assert.False(t, (*Session)(nil).AccessTokenValid())
}

func TestSessionValid(t *testing.T) {
	future := &Session{SessionExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	past := &Session{SessionExpiresAt: time.Now().Add(-time.Hour).Format(time.RFC3339)}

	assert.True(t, future.SessionValid())
	assert.False(t, past.SessionValid())
}
