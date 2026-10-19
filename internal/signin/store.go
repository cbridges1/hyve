package signin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

const (
	// UserPrefix and GroupPrefix are what Kubernetes sees: a User "alice"
	// in group "ops" is impersonated as user "hyve:alice", group "hyve:ops".
	UserPrefix  = "hyve:"
	GroupPrefix = "hyve:"

	// MinPasswordLength matches Rancher's default password-min-length.
	MinPasswordLength = 12

	// DefaultSessionTTL matches Rancher's auth-user-session-ttl-minutes.
	DefaultSessionTTL = 16 * time.Hour

	// BootstrapAdmin is the User the host creates on first start.
	BootstrapAdmin = "admin"

	labelPasswordFor = "hyve.io/password-for"
	labelSessionFor  = "hyve.io/session-for"
	keyHash          = "hash"
	keyExpiresAt     = "expiresAt"
	keyBootstrap     = "bootstrapPassword"
	identityCacheTTL = 30 * time.Second
	bcryptCost       = 12
)

var (
	// ErrInvalidCredentials covers an unknown user, a wrong password and a
	// disabled user alike, so a caller can't tell which.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrMustChangePassword means the password was right but has to be
	// changed before a session is issued.
	ErrMustChangePassword = errors.New("password must be changed")
	// ErrInvalidSession is any session that's malformed, unknown, expired
	// or belongs to a disabled or deleted user.
	ErrInvalidSession = errors.New("invalid or expired session")
	// ErrInvalidNewPassword means a password change was refused by the
	// password policy.
	ErrInvalidNewPassword = errors.New("invalid new password")

	usernamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// ValidUsername reports whether name can be a User's name. The CRD enforces
// the same rule.
func ValidUsername(name string) bool {
	return len(name) <= 63 && usernamePattern.MatchString(name)
}

// ValidatePassword applies the password policy.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > 72 {
		return errors.New("password must be at most 72 bytes") // bcrypt's limit
	}
	return nil
}

// Identity is a signed-in user as Kubernetes should see it.
type Identity struct {
	Username  string
	Groups    []string
	SessionID string
}

// KubeUser is the impersonated Kubernetes username.
func (i Identity) KubeUser() string { return UserPrefix + i.Username }

// KubeGroups are the impersonated Kubernetes groups.
func (i Identity) KubeGroups() []string {
	groups := make([]string, 0, len(i.Groups)+1)
	for _, g := range i.Groups {
		groups = append(groups, GroupPrefix+g)
	}
	return append(groups, "system:authenticated")
}

// Store keeps Users' password hashes and sessions as Secrets in Namespace.
type Store struct {
	Client     client.Client
	Namespace  string
	SessionTTL time.Duration
	Now        func() time.Time

	mu    sync.Mutex
	cache map[string]cachedIdentity
}

type cachedIdentity struct {
	identity Identity
	until    time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) ttl() time.Duration {
	if s.SessionTTL > 0 {
		return s.SessionTTL
	}
	return DefaultSessionTTL
}

func passwordSecretName(username string) string { return "hyve-password-" + username }
func sessionSecretName(id string) string        { return "hyve-session-" + id }

func ownerRef(u *hyvev1alpha1.User) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: hyvev1alpha1.GroupVersion.String(),
		Kind:       "User",
		Name:       u.Name,
		UID:        u.UID,
	}
}

// getUser returns the named User.
func (s *Store) getUser(ctx context.Context, username string) (*hyvev1alpha1.User, error) {
	var u hyvev1alpha1.User
	if err := s.Client.Get(ctx, client.ObjectKey{Name: username}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// checkPassword verifies username's password.
func (s *Store) checkPassword(ctx context.Context, username, password string) (*hyvev1alpha1.User, error) {
	if !ValidUsername(username) {
		return nil, ErrInvalidCredentials
	}
	u, err := s.getUser(ctx, username)
	if apierrors.IsNotFound(err) {
		// Spend the same time as a real comparison.
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	var sec corev1.Secret
	err = s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: passwordSecretName(username)}, &sec)
	if apierrors.IsNotFound(err) {
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword(sec.Data[keyHash], []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	if u.Spec.Disabled {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// dummyHash is compared against when there's no real hash, so a missing
// user takes as long as a wrong password. Computed lazily: bcrypt at cost 12
// takes a noticeable fraction of a second, and every CLI run links this
// package.
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcryptCost)
	return h
})

// Login checks a username and password and returns a new session. When the
// User must change its password, newPassword has to be given; it's set
// before the session is issued.
func (s *Store) Login(ctx context.Context, username, password, newPassword string) (token string, expiresAt time.Time, err error) {
	u, err := s.checkPassword(ctx, username, password)
	if err != nil {
		return "", time.Time{}, err
	}
	if u.Spec.MustChangePassword || newPassword != "" {
		if newPassword == "" {
			return "", time.Time{}, ErrMustChangePassword
		}
		if newPassword == password {
			return "", time.Time{}, fmt.Errorf("%w: it must differ from the current one", ErrInvalidNewPassword)
		}
		if err := ValidatePassword(newPassword); err != nil {
			return "", time.Time{}, fmt.Errorf("%w: %v", ErrInvalidNewPassword, err)
		}
		if err := s.SetPassword(ctx, username, newPassword); err != nil {
			return "", time.Time{}, err
		}
		if u, err = s.getUser(ctx, username); err != nil {
			return "", time.Time{}, err
		}
	}
	token, expiresAt, err = s.createSession(ctx, u)
	if err != nil {
		return "", time.Time{}, err
	}
	now := metav1.NewTime(s.now())
	patch := client.MergeFrom(u.DeepCopy())
	u.Status.LastLogin = &now
	if err := s.Client.Status().Patch(ctx, u, patch); err != nil {
		log.Printf("signin: recording last login for %s: %v", username, err)
	}
	return token, expiresAt, nil
}

// SetPassword stores a new bcrypt hash for username and clears
// MustChangePassword.
func (s *Store) SetPassword(ctx context.Context, username, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	u, err := s.getUser(ctx, username)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: passwordSecretName(username)}}
	err = s.Client.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	switch {
	case apierrors.IsNotFound(err):
		sec.Labels = map[string]string{labelPasswordFor: username}
		sec.OwnerReferences = []metav1.OwnerReference{ownerRef(u)}
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = map[string][]byte{keyHash: hash}
		if err := s.Client.Create(ctx, sec); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		sec.Data = map[string][]byte{keyHash: hash}
		if err := s.Client.Update(ctx, sec); err != nil {
			return err
		}
	}
	if u.Spec.MustChangePassword {
		patch := client.MergeFrom(u.DeepCopy())
		u.Spec.MustChangePassword = false
		return s.Client.Patch(ctx, u, patch)
	}
	return nil
}

func (s *Store) createSession(ctx context.Context, u *hyvev1alpha1.User) (string, time.Time, error) {
	token, id, hash, err := newSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := s.now().Add(s.ttl()).UTC().Truncate(time.Second)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       s.Namespace,
			Name:            sessionSecretName(id),
			Labels:          map[string]string{labelSessionFor: u.Name},
			OwnerReferences: []metav1.OwnerReference{ownerRef(u)},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			keyHash:      []byte(hash),
			keyExpiresAt: []byte(expiresAt.Format(time.RFC3339)),
		},
	}
	if err := s.Client.Create(ctx, sec); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// Lookup returns the identity behind a session token.
func (s *Store) Lookup(ctx context.Context, token string) (Identity, error) {
	id, secret, ok := parseSessionToken(token)
	if !ok {
		return Identity{}, ErrInvalidSession
	}
	hash := hashSecret(secret)
	now := s.now()

	s.mu.Lock()
	if c, ok := s.cache[hash]; ok && now.Before(c.until) {
		s.mu.Unlock()
		return c.identity, nil
	}
	s.mu.Unlock()

	var sec corev1.Secret
	err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: sessionSecretName(id)}, &sec)
	if apierrors.IsNotFound(err) {
		return Identity{}, ErrInvalidSession
	}
	if err != nil {
		return Identity{}, err
	}
	if !hashesEqual(string(sec.Data[keyHash]), hash) {
		return Identity{}, ErrInvalidSession
	}
	expiresAt, err := time.Parse(time.RFC3339, string(sec.Data[keyExpiresAt]))
	if err != nil || !now.Before(expiresAt) {
		return Identity{}, ErrInvalidSession
	}
	username := sec.Labels[labelSessionFor]
	u, err := s.getUser(ctx, username)
	if apierrors.IsNotFound(err) {
		return Identity{}, ErrInvalidSession
	}
	if err != nil {
		return Identity{}, err
	}
	if u.Spec.Disabled {
		return Identity{}, ErrInvalidSession
	}
	ident := Identity{Username: u.Name, Groups: append([]string(nil), u.Spec.Groups...), SessionID: id}

	until := now.Add(identityCacheTTL)
	if expiresAt.Before(until) {
		until = expiresAt
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cachedIdentity{}
	}
	s.cache[hash] = cachedIdentity{identity: ident, until: until}
	s.mu.Unlock()
	return ident, nil
}

// Logout deletes the session behind token.
func (s *Store) Logout(ctx context.Context, token string) error {
	id, secret, ok := parseSessionToken(token)
	if !ok {
		return ErrInvalidSession
	}
	s.forget(hashSecret(secret))
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: sessionSecretName(id)}}
	return client.IgnoreNotFound(s.Client.Delete(ctx, sec))
}

// RevokeSessions deletes every session of username except keepID.
func (s *Store) RevokeSessions(ctx context.Context, username, keepID string) error {
	var list corev1.SecretList
	if err := s.Client.List(ctx, &list, client.InNamespace(s.Namespace), client.MatchingLabels{labelSessionFor: username}); err != nil {
		return err
	}
	for i := range list.Items {
		sec := &list.Items[i]
		if keepID != "" && sec.Name == sessionSecretName(keepID) {
			continue
		}
		s.forget(string(sec.Data[keyHash]))
		if err := client.IgnoreNotFound(s.Client.Delete(ctx, sec)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) forget(hash string) {
	s.mu.Lock()
	delete(s.cache, hash)
	s.mu.Unlock()
}

// DeleteExpiredSessions removes session Secrets past their expiry.
func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	var list corev1.SecretList
	if err := s.Client.List(ctx, &list, client.InNamespace(s.Namespace), client.HasLabels{labelSessionFor}); err != nil {
		return err
	}
	now := s.now()
	for i := range list.Items {
		sec := &list.Items[i]
		expiresAt, err := time.Parse(time.RFC3339, string(sec.Data[keyExpiresAt]))
		if err == nil && now.Before(expiresAt) {
			continue
		}
		if err := client.IgnoreNotFound(s.Client.Delete(ctx, sec)); err != nil {
			return err
		}
	}
	return nil
}

// EnsureBootstrapAdmin creates the admin User on first start, as Rancher
// creates its admin: the password comes from bootstrapSecret's
// "bootstrapPassword" key, which is generated if the Secret doesn't exist,
// and must be changed at first sign-in. It does nothing once admin exists.
func (s *Store) EnsureBootstrapAdmin(ctx context.Context, bootstrapSecret string) error {
	_, err := s.getUser(ctx, BootstrapAdmin)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: bootstrapSecret}}
	err = s.Client.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	switch {
	case apierrors.IsNotFound(err):
		pw, err := randomPassword()
		if err != nil {
			return err
		}
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = map[string][]byte{keyBootstrap: []byte(pw)}
		if err := s.Client.Create(ctx, sec); err != nil {
			return err
		}
		log.Printf("signin: generated the bootstrap password for %q; read it with: kubectl get secret -n %s %s -o go-template='{{.data.%s|base64decode}}'",
			BootstrapAdmin, s.Namespace, bootstrapSecret, keyBootstrap)
	case err != nil:
		return err
	}
	password := string(sec.Data[keyBootstrap])
	if len(password) == 0 {
		return fmt.Errorf("secret %s/%s has no %s", s.Namespace, bootstrapSecret, keyBootstrap)
	}

	u := &hyvev1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: BootstrapAdmin},
		Spec:       hyvev1alpha1.UserSpec{DisplayName: "Default Admin", MustChangePassword: true},
	}
	if err := s.Client.Create(ctx, u); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	// SetPassword clears MustChangePassword, so set it again afterwards.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	if u, err = s.getUser(ctx, BootstrapAdmin); err != nil {
		return err
	}
	pwSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       s.Namespace,
			Name:            passwordSecretName(BootstrapAdmin),
			Labels:          map[string]string{labelPasswordFor: BootstrapAdmin},
			OwnerReferences: []metav1.OwnerReference{ownerRef(u)},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{keyHash: hash},
	}
	if err := s.Client.Create(ctx, pwSec); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	log.Printf("signin: created User %q; it must change its password at first sign-in", BootstrapAdmin)
	return nil
}
