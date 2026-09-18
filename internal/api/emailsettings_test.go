package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEmailSettingsTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerEmailSettingsRoutes(mux)
	return mux
}

func doEmailSettingsRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	ctx := contextWithRole(req.Context(), role)
	ctx = contextWithUsername(ctx, "caller")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	newEmailSettingsTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleGetEmailSettings_AdminForbidden(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/system/email", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code, "install-wide email settings are superadmin-only, not admin")
}

func TestHandleGetEmailSettings_Unconfigured(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodGet, "/system/email", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto emailSettingsDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.False(t, dto.Configured)
	assert.False(t, dto.PasswordSet)
}

func TestHandleUpdateEmailSettings_SavesAndNeverEchoesPassword(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/system/email", updateEmailSettingsRequest{
		SMTPHost:     strPtr("smtp.example.com"),
		SMTPPort:     intPtr(587),
		SMTPUsername: strPtr("relay"),
		SMTPPassword: strPtr("s3cret"),
		FromAddress:  strPtr("no-reply@example.com"),
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "s3cret", "the plaintext password must never appear in the response")

	var dto emailSettingsDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "smtp.example.com", dto.SMTPHost)
	assert.True(t, dto.PasswordSet)
	assert.True(t, dto.Configured)

	stored, err := s.OrgStore.GetEmailSettings(t.Context())
	require.NoError(t, err)
	require.NotNil(t, stored.SMTPPassword)
	assert.Equal(t, "s3cret", *stored.SMTPPassword)
}

func TestHandleUpdateEmailSettings_PartialUpdateLeavesPasswordUntouched(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/system/email", updateEmailSettingsRequest{
		SMTPHost:     strPtr("smtp.example.com"),
		SMTPUsername: strPtr("relay"),
		SMTPPassword: strPtr("s3cret"),
		FromAddress:  strPtr("no-reply@example.com"),
	})
	require.Equal(t, http.StatusOK, rec.Code)

	// Second PATCH flips only SkipVerify — omits SMTPPassword entirely.
	rec = doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/system/email", updateEmailSettingsRequest{
		SkipVerify: boolPtr(true),
	})
	require.Equal(t, http.StatusOK, rec.Code)

	stored, err := s.OrgStore.GetEmailSettings(t.Context())
	require.NoError(t, err)
	require.NotNil(t, stored.SMTPPassword, "an omitted password field on PATCH must leave the stored one untouched")
	assert.Equal(t, "s3cret", *stored.SMTPPassword)
	assert.True(t, stored.SkipVerify)
	assert.Equal(t, "smtp.example.com", stored.SMTPHost, "fields not in this PATCH must survive too")
}

func TestHandleUpdateEmailSettings_EmptyStringClearsPassword(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/system/email", updateEmailSettingsRequest{
		SMTPHost:     strPtr("smtp.example.com"),
		SMTPPassword: strPtr("s3cret"),
	})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/system/email", updateEmailSettingsRequest{
		SMTPPassword: strPtr(""),
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto emailSettingsDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.False(t, dto.PasswordSet, "an explicit empty string must clear the stored password")

	stored, err := s.OrgStore.GetEmailSettings(t.Context())
	require.NoError(t, err)
	assert.Nil(t, stored.SMTPPassword)
}

func TestHandleTestEmail_NotConfigured_400(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPost, "/system/email/test", testEmailRequest{To: "someone@example.com"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleTestEmail_MissingTo_400(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPost, "/system/email/test", testEmailRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleTestEmail_AdminForbidden(t *testing.T) {
	s := newTestServer(t)
	rec := doEmailSettingsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/system/email/test", testEmailRequest{To: "someone@example.com"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func intPtr(i int) *int    { return &i }
func boolPtr(b bool) *bool { return &b }
