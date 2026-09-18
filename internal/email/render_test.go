package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRender_AllTemplates(t *testing.T) {
	cases := []struct {
		tmpl             Template
		data             any
		wantSubject      string
		wantBodyContains []string
	}{
		{
			tmpl:        TemplatePasswordResetCode,
			data:        PasswordResetCodeData{Username: "alice", Code: "ABC12345", Link: "https://hyve.example.com/#/reset-password?email=alice@example.com&token=ABC12345"},
			wantSubject: "Reset your password",
			wantBodyContains: []string{
				"Hi alice",
				"ABC12345",
				"https://hyve.example.com/#/reset-password?email=alice@example.com&amp;token=ABC12345",
			},
		},
		{
			tmpl:             TemplatePasswordChanged,
			data:             PasswordChangedData{Username: "alice"},
			wantSubject:      "Your password was changed",
			wantBodyContains: []string{"Hi alice", "just changed"},
		},
		{
			tmpl:             TemplateRoleChanged,
			data:             RoleChangedData{Username: "alice", OldRole: "read-only", NewRole: "admin"},
			wantSubject:      "Your account role was changed",
			wantBodyContains: []string{"read-only", "admin"},
		},
		{
			tmpl:             TemplateEmailChanged,
			data:             EmailChangedData{Username: "alice", NewEmail: "new@example.com"},
			wantSubject:      "Your account email was changed",
			wantBodyContains: []string{"new@example.com"},
		},
		{
			tmpl:             TemplateAccountCreated,
			data:             AccountCreatedData{Username: "bob", Role: "read-only"},
			wantSubject:      "An account was created for you",
			wantBodyContains: []string{"bob", "read-only"},
		},
		{
			tmpl:             TemplateTest,
			data:             TestEmailData{RequestedBy: "jbridges"},
			wantSubject:      "Test email from hyve",
			wantBodyContains: []string{"jbridges"},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.tmpl), func(t *testing.T) {
			subject, body, err := render(tc.tmpl, tc.data)
			require.NoError(t, err)
			assert.Equal(t, tc.wantSubject, subject)
			for _, want := range tc.wantBodyContains {
				assert.Contains(t, body, want)
			}
			assert.Contains(t, body, "<html>", "body must be a full HTML document via the shared layout")
			assert.NotContains(t, subject, "<", "subject must never contain markup")
		})
	}
}

func TestRender_EscapesUserSuppliedContent(t *testing.T) {
	_, body, err := render(TemplateAccountCreated, AccountCreatedData{
		Username: `<script>alert(1)</script>`,
		Role:     "read-only",
	})
	require.NoError(t, err)
	assert.False(t, strings.Contains(body, "<script>alert(1)</script>"), "html/template must escape user-supplied content, not let it inject raw markup")
	assert.Contains(t, body, "&lt;script&gt;")
}

func TestRender_UnknownTemplate_Errors(t *testing.T) {
	_, _, err := render(Template("does-not-exist"), nil)
	assert.Error(t, err)
}
