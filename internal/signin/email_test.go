package signin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/email"
)

type sent struct {
	to   string
	tmpl email.Template
	data any
}

// mailbox records what a Server sends.
type mailbox struct {
	mu   sync.Mutex
	got  []sent
	wake chan struct{}
}

func newMailbox(srv *Server) *mailbox {
	m := &mailbox{wake: make(chan struct{}, 100)}
	srv.Mailer = func(_ context.Context, _ email.Settings, to string, tmpl email.Template, data any) error {
		m.mu.Lock()
		m.got = append(m.got, sent{to, tmpl, data})
		m.mu.Unlock()
		m.wake <- struct{}{}
		return nil
	}
	return m
}

// wait returns the next n emails, sorted by send.
func (m *mailbox) wait(t *testing.T, n int) []sent {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-m.wake:
		case <-time.After(5 * time.Second):
			t.Fatalf("waited for %d emails, got %d", n, i)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.got
	m.got = nil
	return out
}

// none checks nothing more was sent.
func (m *mailbox) none(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Empty(t, m.got)
}

func smtpOn(t *testing.T, s *Store) {
	t.Helper()
	require.NoError(t, s.SaveEmailSettings(context.Background(), email.Settings{Host: "smtp.example.com", Port: 587, FromAddress: "hyve@example.com"}))
}

func setEmail(t *testing.T, s *Store, name, addr string) {
	t.Helper()
	u, err := s.getUser(context.Background(), name)
	require.NoError(t, err)
	u.Spec.Email = addr
	require.NoError(t, s.Client.Update(context.Background(), u))
}

func getJSON(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEmailSettingsEndpoints(t *testing.T) {
	allow := func(sar *authorizationv1.SubjectAccessReview) bool {
		ra := sar.Spec.ResourceAttributes
		return sar.Spec.User == "hyve:admin" && ra.Resource == "secrets" && ra.Name == EmailSecret && ra.Namespace == ns
	}
	s, _ := newStore(t, allow)
	ctx := context.Background()
	addUser(t, s, "admin", "admin-password-123")
	addUser(t, s, "alice", "correct-horse-battery")
	srv := NewServer(s, nil, "test")
	box := newMailbox(srv)
	h := srv.Handler()
	admin, _, err := s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)
	alice, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, getJSON(t, h, "/v1/settings/email", alice).Code)
	assert.Equal(t, http.StatusForbidden, putJSON(t, h, "/v1/settings/email", alice, EmailSettingsUpdate{}).Code)

	rec := getJSON(t, h, "/v1/settings/email", admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"host":"","port":587,"username":"","passwordSet":false,"useTLS":false,"skipVerify":false,"fromAddress":"","fromName":"","configured":false}`, rec.Body.String())

	rec = postJSON(t, h, "/v1/settings/email/test", admin, map[string]string{"to": "a@example.com"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "NotConfigured", code(t, rec))

	host, port, user, pw, from := "smtp.example.com", 2525, "relay", "s3cret-relay", "hyve@example.com"
	rec = putJSON(t, h, "/v1/settings/email", admin, EmailSettingsUpdate{Host: &host})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "a host without a from address")
	rec = putJSON(t, h, "/v1/settings/email", admin, EmailSettingsUpdate{Host: &host, Port: &port, Username: &user, Password: &pw, FromAddress: &from})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), pw, "the password is never sent back")
	var v EmailSettingsView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.True(t, v.PasswordSet)
	assert.True(t, v.Configured)

	// Leaving the password out keeps it.
	name := "Hyve"
	require.Equal(t, http.StatusOK, putJSON(t, h, "/v1/settings/email", admin, EmailSettingsUpdate{FromName: &name}).Code)
	e, err := s.EmailSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, email.Settings{Host: host, Port: port, Username: user, Password: pw, FromAddress: from, FromName: name}, e)

	rec = postJSON(t, h, "/v1/settings/email/test", admin, map[string]string{"to": "a@example.com"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := box.wait(t, 1)
	assert.Equal(t, "a@example.com", got[0].to)
	assert.Equal(t, email.TemplateTest, got[0].tmpl)
	assert.Equal(t, email.TestEmailData{RequestedBy: "admin"}, got[0].data)
}

func TestSeedEmailSettings(t *testing.T) {
	s, _ := newStore(t, nil)
	ctx := context.Background()
	require.NoError(t, s.SeedEmailSettings(ctx, email.Settings{Host: "first", FromAddress: "a@x"}))
	require.NoError(t, s.SeedEmailSettings(ctx, email.Settings{Host: "second", FromAddress: "b@x"}))
	e, err := s.EmailSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, "first", e.Host, "seeding never overwrites saved settings")
}

func TestPasswordReset(t *testing.T) {
	resetDelay = func(context.Context) {}
	t.Cleanup(func() { resetDelay = randomDelay })
	s, clk := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "alice", "correct-horse-battery")
	setEmail(t, s, "alice", "alice@example.com")
	addUser(t, s, "bob", "bobs-own-password")
	smtpOn(t, s)
	srv := NewServer(s, nil, "test")
	srv.PublicURL = "https://hyve.example.com/"
	box := newMailbox(srv)
	h := srv.Handler()
	session, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	// Unknown users and users without an address get the same answer, and
	// no email.
	for _, id := range []string{"nobody", "nobody@example.com", "bob"} {
		rec := postJSON(t, h, "/v1-public/password-reset", "", ResetRequest{Identifier: id})
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"sent":true}`, rec.Body.String())
	}
	box.none(t)

	// By address, any case.
	rec := postJSON(t, h, "/v1-public/password-reset", "", ResetRequest{Identifier: "Alice@Example.com"})
	require.Equal(t, http.StatusOK, rec.Code)
	got := box.wait(t, 1)
	require.Equal(t, "alice@example.com", got[0].to)
	data := got[0].data.(email.PasswordResetCodeData)
	assert.Regexp(t, regexp.MustCompile(`^[A-Z2-9]{8}$`), data.Code)
	assert.Equal(t, "https://hyve.example.com/reset-password?code="+data.Code+"&user=alice", data.Link)

	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "alice", Code: "WRONGONE", NewPassword: "a-brand-new-password"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "InvalidResetCode", code(t, rec))
	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "alice", Code: data.Code, NewPassword: "short"})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	// The code works once, lower case too, and signs alice out.
	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "alice", Code: " " + regexp.MustCompile(`.`).ReplaceAllStringFunc(data.Code, func(c string) string { return string(c[0] | 0x20) }) + " ", NewPassword: "a-brand-new-password"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err = s.Lookup(ctx, session)
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, _, err = s.Login(ctx, "alice", "a-brand-new-password", "")
	assert.NoError(t, err)
	got = box.wait(t, 1)
	assert.Equal(t, sent{"alice@example.com", email.TemplatePasswordChanged, email.PasswordChangedData{Username: "alice"}}, got[0])
	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "alice", Code: data.Code, NewPassword: "yet-another-password"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a code is used up")

	// Codes expire.
	postJSON(t, h, "/v1-public/password-reset", "", ResetRequest{Identifier: "alice"})
	data = box.wait(t, 1)[0].data.(email.PasswordResetCodeData)
	clk.t = clk.t.Add(ResetCodeTTL + time.Second)
	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "alice", Code: data.Code, NewPassword: "yet-another-password"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	require.NoError(t, s.DeleteExpiredResetCodes(ctx))
	var list corev1.SecretList
	require.NoError(t, s.Client.List(ctx, &list, client.HasLabels{labelResetFor}))
	assert.Empty(t, list.Items)

	// Without a public address the email has only the code.
	srv.PublicURL = ""
	postJSON(t, h, "/v1-public/password-reset", "", ResetRequest{Identifier: "alice"})
	assert.Empty(t, box.wait(t, 1)[0].data.(email.PasswordResetCodeData).Link)

	// An identity provider's user has no password to reset.
	require.NoError(t, s.Client.Create(ctx, &hyvev1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "sso"}, Spec: hyvev1alpha1.UserSpec{Provider: "oidc", Email: "sso@example.com"}}))
	postJSON(t, h, "/v1-public/password-reset", "", ResetRequest{Identifier: "sso@example.com"})
	box.none(t)

	srv.NoPasswords = true
	assert.Equal(t, http.StatusForbidden, postJSON(t, h, "/v1-public/password-reset", "", ResetRequest{Identifier: "alice"}).Code)
}

func TestPasswordChangeNotices(t *testing.T) {
	allow := func(sar *authorizationv1.SubjectAccessReview) bool { return sar.Spec.User == "hyve:admin" }
	s, _ := newStore(t, allow)
	ctx := context.Background()
	addUser(t, s, "admin", "admin-password-123")
	addUser(t, s, "alice", "correct-horse-battery")
	setEmail(t, s, "alice", "alice@example.com")
	smtpOn(t, s)
	srv := NewServer(s, nil, "test")
	box := newMailbox(srv)
	h := srv.Handler()
	admin, _, err := s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, putJSON(t, h, "/v1/users/alice/password", admin, SetPasswordRequest{NewPassword: "reset-by-the-admin"}).Code)
	assert.Equal(t, "alice@example.com", box.wait(t, 1)[0].to)

	// A forced change at sign-in is a change too.
	u, err := s.getUser(ctx, "alice")
	require.NoError(t, err)
	u.Spec.MustChangePassword = true
	require.NoError(t, s.Client.Update(ctx, u))
	rec := postJSON(t, h, "/v1-public/login", "", LoginRequest{Username: "alice", Password: "reset-by-the-admin", NewPassword: "chosen-by-alice-now"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, email.TemplatePasswordChanged, box.wait(t, 1)[0].tmpl)

	// No address, no email.
	require.Equal(t, http.StatusOK, putJSON(t, h, "/v1/users/admin/password", admin, SetPasswordRequest{CurrentPassword: "admin-password-123", NewPassword: "admin-password-456"}).Code)
	box.none(t)
}

func TestUserNotices(t *testing.T) {
	s, clk := newStore(t, nil)
	ctx := context.Background()
	smtpOn(t, s)
	srv := NewServer(s, nil, "test")
	box := newMailbox(srv)
	notice := func(name string) { require.NoError(t, srv.noticeUser(ctx, s.Client, name)) }
	create := func(u *hyvev1alpha1.User) {
		u.CreationTimestamp = metav1.NewTime(clk.t)
		require.NoError(t, s.Client.Create(ctx, u))
	}

	// A new user with an address is told once.
	create(&hyvev1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}, Spec: hyvev1alpha1.UserSpec{Email: "alice@example.com", Groups: []string{"ops"}}})
	notice("alice")
	notice("alice")
	got := box.wait(t, 1)
	assert.Equal(t, sent{"alice@example.com", email.TemplateAccountCreated, email.AccountCreatedData{Username: "alice", Groups: "ops"}}, got[0])
	box.none(t)

	// Groups and address changes: the address change goes to the old one.
	u, err := s.getUser(ctx, "alice")
	require.NoError(t, err)
	u.Spec.Groups = []string{"dev", "ops"}
	u.Spec.Email = "alice@new.example.com"
	require.NoError(t, s.Client.Update(ctx, u))
	notice("alice")
	got = box.wait(t, 2)
	assert.ElementsMatch(t, []sent{
		{"alice@example.com", email.TemplateEmailChanged, email.EmailChangedData{Username: "alice", NewEmail: "alice@new.example.com"}},
		{"alice@new.example.com", email.TemplateGroupsChanged, email.GroupsChangedData{Username: "alice", OldGroups: "ops", NewGroups: "dev, ops"}},
	}, got)
	notice("alice")
	box.none(t)

	// Users from before (older than the window) and identity provider users
	// aren't told their account exists.
	create(&hyvev1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "sso"}, Spec: hyvev1alpha1.UserSpec{Provider: "oidc", Email: "sso@example.com"}})
	notice("sso")
	clk.t = clk.t.Add(newUserWindow + time.Minute)
	require.NoError(t, s.Client.Create(ctx, &hyvev1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "old", CreationTimestamp: metav1.NewTime(clk.t.Add(-time.Hour))},
		Spec:       hyvev1alpha1.UserSpec{Email: "old@example.com"},
	}))
	notice("old")
	box.none(t)

	// An identity provider changing its user's address isn't news.
	u, err = s.getUser(ctx, "sso")
	require.NoError(t, err)
	u.Spec.Email = "sso2@example.com"
	require.NoError(t, s.Client.Update(ctx, u))
	notice("sso")
	box.none(t)

	// The record goes with the user.
	var sec corev1.Secret
	require.NoError(t, s.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: noticesSecretName("alice")}, &sec))
	assert.Equal(t, "alice", sec.OwnerReferences[0].Name)
}

func TestInvite(t *testing.T) {
	allow := func(sar *authorizationv1.SubjectAccessReview) bool {
		ra := sar.Spec.ResourceAttributes
		return sar.Spec.User == "hyve:admin" && ra.Resource == "users" && ra.Subresource == "password" && ra.Verb == "update"
	}
	s, clk := newStore(t, allow)
	ctx := context.Background()
	addUser(t, s, "admin", "admin-password-123")
	addUser(t, s, "alice", "correct-horse-battery")
	srv := NewServer(s, nil, "test")
	srv.PublicURL = "https://hyve.example.com"
	box := newMailbox(srv)
	h := srv.Handler()
	admin, _, err := s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)
	alice, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	// An invited user: no password yet, and no "account created" email.
	require.NoError(t, s.Client.Create(ctx, &hyvev1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "bob", Annotations: map[string]string{InviteAnnotation: "true"}, CreationTimestamp: metav1.NewTime(clk.t)},
		Spec:       hyvev1alpha1.UserSpec{Email: "bob@example.com", Groups: []string{"ops"}},
	}))
	smtpOn(t, s)
	require.NoError(t, srv.noticeUser(ctx, s.Client, "bob"))
	box.none(t)

	// Inviting takes the right to set the password.
	assert.Equal(t, http.StatusForbidden, postJSON(t, h, "/v1/users/bob/invite", alice, struct{}{}).Code)
	assert.Equal(t, http.StatusNotFound, postJSON(t, h, "/v1/users/nobody/invite", admin, struct{}{}).Code)
	rec := postJSON(t, h, "/v1/users/admin/invite", admin, struct{}{})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "no address")

	rec = postJSON(t, h, "/v1/users/bob/invite", admin, struct{}{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := box.wait(t, 1)
	require.Equal(t, "bob@example.com", got[0].to)
	require.Equal(t, email.TemplateInvite, got[0].tmpl)
	inv := got[0].data.(email.InviteData)
	assert.Equal(t, "ops", inv.Groups)
	assert.Equal(t, "https://hyve.example.com/reset-password?code="+inv.Code+"&invite=1&user=bob", inv.Link)

	// The code still works after the 2 hours a reset gets, and sets bob's
	// first password.
	clk.t = clk.t.Add(3 * 24 * time.Hour)
	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "bob", Code: inv.Code, NewPassword: "bobs-chosen-password"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, _, err = s.Login(ctx, "bob", "bobs-chosen-password", "")
	assert.NoError(t, err)
	assert.Equal(t, email.TemplatePasswordChanged, box.wait(t, 1)[0].tmpl)

	// Expired after 7 days. (The clock moved past admin's session.)
	admin, _, err = s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, postJSON(t, h, "/v1/users/bob/invite", admin, struct{}{}).Code)
	inv = box.wait(t, 1)[0].data.(email.InviteData)
	clk.t = clk.t.Add(InviteTTL + time.Minute)
	rec = postJSON(t, h, "/v1-public/password-reset/confirm", "", ResetConfirm{Identifier: "bob", Code: inv.Code, NewPassword: "another-password-1"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Without SMTP an invitation can't go, and says so.
	require.NoError(t, s.SaveEmailSettings(ctx, email.Settings{}))
	admin, _, err = s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)
	rec = postJSON(t, h, "/v1/users/bob/invite", admin, struct{}{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "NotConfigured", code(t, rec))
}
