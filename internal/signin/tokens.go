package signin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// API tokens are sessions that last longer and have a name: the same
// "hs-" token, kept the same way (a Secret holding its hash, owned by the
// User), so everywhere a session works a token does. They're for scripts
// and CI. A token isn't signed out by a password change (revoke it, or
// disable the user), and it can't make more tokens, so one that leaks
// can't outlive its expiry.
//
// A session from a CI job's ID token (TrustedCI, federation.go) is kept
// the same way too, marked as such, and lasts an hour.

const (
	// labelTokenName marks a session as an API token, and names it.
	labelTokenName = "hyve.io/token-name"
	// annotationSessionKind is "federated" on a CI job's session.
	annotationSessionKind = "hyve.io/session-kind"
	annotationDescription = "hyve.io/description"
	annotationCreatedBy   = "hyve.io/created-by"
	annotationLastUsed    = "hyve.io/last-used"
	// annotationVia is the TrustedCI entry and token subject a CI job's
	// session came from.
	annotationVia = "hyve.io/via"

	// MaxTokenTTL is the longest an API token may last.
	MaxTokenTTL = 366 * 24 * time.Hour
	// DefaultTokenTTL is how long one lasts unless told.
	DefaultTokenTTL = 90 * 24 * time.Hour
	// lastUsedEvery is how often a token's last use is written down.
	lastUsedEvery = time.Hour
)

// Session kinds, on an Identity.
const (
	KindSession   = ""
	KindToken     = "token"
	KindFederated = "federated"
)

var (
	// ErrTokenExists is a second token of the same name for a user.
	ErrTokenExists = errors.New("the user already has a token of that name")
	// ErrNoToken is a token that doesn't exist.
	ErrNoToken = errors.New("no such token")
	// ErrInvalidToken wraps what's wrong with a token asked for (its
	// name, expiry or description).
	ErrInvalidToken = errors.New("invalid token")
)

func invalidToken(msg string) error { return fmt.Errorf("%w: %s", ErrInvalidToken, msg) }

// TokenInfo describes an API token; the token itself is shown once, when
// it's made.
type TokenInfo struct {
	User        string     `json:"user"`
	Name        string     `json:"name"`
	ID          string     `json:"id"`
	Description string     `json:"description,omitempty"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	LastUsed    *time.Time `json:"lastUsed,omitempty"`
}

// CreateToken makes an API token for user, lasting ttl. by is who asked.
func (s *Store) CreateToken(ctx context.Context, user, name, description, by string, ttl time.Duration) (string, TokenInfo, error) {
	if !ValidUsername(name) {
		return "", TokenInfo{}, invalidToken("a token name must be a DNS label: lowercase letters, digits and '-', at most 63 characters")
	}
	if ttl <= 0 || ttl > MaxTokenTTL {
		return "", TokenInfo{}, invalidToken(fmt.Sprintf("a token must expire, within %d days", int(MaxTokenTTL.Hours()/24)))
	}
	if len(description) > 256 {
		return "", TokenInfo{}, invalidToken("the description is longer than 256 characters")
	}
	u, err := s.getUser(ctx, user)
	if err != nil {
		return "", TokenInfo{}, err
	}
	if u.Spec.Disabled {
		return "", TokenInfo{}, invalidToken("the user is disabled")
	}
	if _, err := s.findToken(ctx, user, name); err == nil {
		return "", TokenInfo{}, ErrTokenExists
	} else if !errors.Is(err, ErrNoToken) {
		return "", TokenInfo{}, err
	}
	token, id, hash, err := newSessionToken()
	if err != nil {
		return "", TokenInfo{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(ttl)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       s.Namespace,
			Name:            sessionSecretName(id),
			Labels:          map[string]string{labelSessionFor: u.Name, labelTokenName: name},
			Annotations:     map[string]string{annotationCreatedBy: by},
			OwnerReferences: []metav1.OwnerReference{ownerRef(u)},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			keyHash:      []byte(hash),
			keyExpiresAt: []byte(expiresAt.Format(time.RFC3339)),
		},
	}
	if description != "" {
		sec.Annotations[annotationDescription] = description
	}
	if err := s.Client.Create(ctx, sec); err != nil {
		return "", TokenInfo{}, err
	}
	return token, TokenInfo{User: u.Name, Name: name, ID: id, Description: description, CreatedBy: by, CreatedAt: now, ExpiresAt: expiresAt}, nil
}

// ListTokens returns user's API tokens, or everyone's when user is "".
func (s *Store) ListTokens(ctx context.Context, user string) ([]TokenInfo, error) {
	var list corev1.SecretList
	sel := client.MatchingLabels{}
	if user != "" {
		sel[labelSessionFor] = user
	}
	if err := s.Client.List(ctx, &list, client.InNamespace(s.Namespace), sel, client.HasLabels{labelTokenName}); err != nil {
		return nil, err
	}
	out := []TokenInfo{}
	for i := range list.Items {
		out = append(out, tokenInfo(&list.Items[i]))
	}
	sort.Slice(out, func(a, b int) bool {
		return out[a].User+"/"+out[a].Name < out[b].User+"/"+out[b].Name
	})
	return out, nil
}

// RevokeToken deletes user's token name.
func (s *Store) RevokeToken(ctx context.Context, user, name string) error {
	sec, err := s.findToken(ctx, user, name)
	if err != nil {
		return err
	}
	s.forget(string(sec.Data[keyHash]))
	return client.IgnoreNotFound(s.Client.Delete(ctx, sec))
}

func (s *Store) findToken(ctx context.Context, user, name string) (*corev1.Secret, error) {
	var list corev1.SecretList
	if err := s.Client.List(ctx, &list, client.InNamespace(s.Namespace),
		client.MatchingLabels{labelSessionFor: user, labelTokenName: name}); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, ErrNoToken
	}
	return &list.Items[0], nil
}

func tokenInfo(sec *corev1.Secret) TokenInfo {
	t := TokenInfo{
		User:        sec.Labels[labelSessionFor],
		Name:        sec.Labels[labelTokenName],
		ID:          sec.Name[len(sessionSecretName("")):],
		Description: sec.Annotations[annotationDescription],
		CreatedBy:   sec.Annotations[annotationCreatedBy],
		CreatedAt:   sec.CreationTimestamp.UTC(),
	}
	t.ExpiresAt, _ = time.Parse(time.RFC3339, string(sec.Data[keyExpiresAt]))
	if lu, err := time.Parse(time.RFC3339, sec.Annotations[annotationLastUsed]); err == nil {
		t.LastUsed = &lu
	}
	return t
}

// sessionKind is what kind of session a Secret holds.
func sessionKind(sec *corev1.Secret) string {
	switch {
	case sec.Labels[labelTokenName] != "":
		return KindToken
	case sec.Annotations[annotationSessionKind] == KindFederated:
		return KindFederated
	}
	return KindSession
}

// noteUse writes down when a token or session was last used, at most
// once an hour, in the background: it's for 'hyve user token list' and
// the sessions list, not for security.
func (s *Store) noteUse(sec *corev1.Secret) {
	now := s.now().UTC().Truncate(time.Second)
	if last, err := time.Parse(time.RFC3339, sec.Annotations[annotationLastUsed]); err == nil && now.Sub(last) < lastUsedEvery {
		return
	}
	key := client.ObjectKeyFromObject(sec)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, annotationLastUsed, now.Format(time.RFC3339)))
		obj := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
		if err := s.Client.Patch(ctx, obj, client.RawPatch("application/merge-patch+json", patch)); err != nil && !apierrors.IsNotFound(err) {
			log.Printf("signin: noting token use: %v", err)
		}
	}()
}
