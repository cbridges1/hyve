package email

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderAll(t *testing.T) {
	cases := []struct {
		tmpl     Template
		data     any
		subject  string
		contains []string
		absent   []string
	}{
		{
			tmpl:     TemplatePasswordResetCode,
			data:     PasswordResetCodeData{Username: "alice", Code: "ABC12345", Link: "https://hyve.example.com/reset-password?user=alice&code=ABC12345"},
			subject:  "Reset your password",
			contains: []string{"Hi alice", "ABC12345", "https://hyve.example.com/reset-password?user=alice&amp;code=ABC12345", "Reset password</a>"},
		},
		{
			tmpl:     TemplatePasswordResetCode,
			data:     PasswordResetCodeData{Username: "alice", Code: "ABC12345"},
			subject:  "Reset your password",
			contains: []string{"ABC12345", "Forgot password?"},
			absent:   []string{"Reset password</a>"},
		},
		{tmpl: TemplatePasswordChanged, data: PasswordChangedData{Username: "alice"}, subject: "Your password was changed", contains: []string{"Hi alice", "just changed"}},
		{
			tmpl:     TemplateGroupsChanged,
			data:     GroupsChangedData{Username: "alice", OldGroups: "none", NewGroups: "ops, dev"},
			subject:  "Your account's groups were changed",
			contains: []string{"none", "ops, dev"},
		},
		{tmpl: TemplateEmailChanged, data: EmailChangedData{Username: "alice", NewEmail: "new@example.com"}, subject: "Your account email was changed", contains: []string{"new@example.com"}},
		{tmpl: TemplateAccountCreated, data: AccountCreatedData{Username: "bob", Groups: "ops"}, subject: "An account was created for you", contains: []string{"bob", "ops"}},
		{tmpl: TemplateAccountCreated, data: AccountCreatedData{Username: "bob"}, subject: "An account was created for you", contains: []string{"bob"}, absent: []string{">Groups<"}},
		{
			tmpl:     TemplateInvite,
			data:     InviteData{Username: "bob", Groups: "ops", Code: "ABCD2345", Link: "https://h/reset-password?code=ABCD2345&invite=1&user=bob", ExpiresIn: "7 days"},
			subject:  "You're invited to hyve",
			contains: []string{"bob", "ops", "ABCD2345", "Set your password</a>", "7 days"},
		},
		{
			tmpl:     TemplateInvite,
			data:     InviteData{Username: "bob", Code: "ABCD2345", ExpiresIn: "7 days"},
			subject:  "You're invited to hyve",
			contains: []string{"I have a code"},
			absent:   []string{"Set your password</a>", ">Groups<"},
		},
		{tmpl: TemplateTest, data: TestEmailData{RequestedBy: "admin"}, subject: "Test email from hyve", contains: []string{"admin"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.tmpl), func(t *testing.T) {
			subject, body, err := render(tc.tmpl, tc.data)
			require.NoError(t, err)
			assert.Equal(t, tc.subject, subject)
			for _, s := range tc.contains {
				assert.Contains(t, body, s)
			}
			for _, s := range tc.absent {
				assert.NotContains(t, body, s)
			}
			assert.Contains(t, body, "<html>")
		})
	}
}

func TestRenderEscapes(t *testing.T) {
	_, body, err := render(TemplateAccountCreated, AccountCreatedData{Username: `<script>alert(1)</script>`})
	require.NoError(t, err)
	assert.NotContains(t, body, "<script>alert(1)</script>")
}

func TestSendNotConfigured(t *testing.T) {
	err := Send(context.Background(), Settings{}, "a@example.com", TemplateTest, TestEmailData{})
	assert.ErrorIs(t, err, ErrNotConfigured)
}
