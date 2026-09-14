package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/cbridges1/hyve/internal/orgdb"
)

const (
	// signingKeyByteLength is how much entropy EnsureSigningKey generates
	// for a fresh key — matches the 32-byte (256-bit) length the old
	// LoadSigningKey doc comment's own `openssl rand -hex 32` example
	// produced, just generated in-process now instead of by an operator.
	signingKeyByteLength = 32
)

// EnsureSigningKey returns hyve-api's own session-signing key for namespace,
// generating and persisting a fresh one on first call (Milestone 10 Part C)
// — the same get-or-create-once idiom cmd/api's ensureControlPlaneOrganization
// already established for the control-plane organization row. This replaces
// the old design, which required an operator to provision a
// hyve-api-credentials Kubernetes Secret by hand before the API would even
// start ("hyve itself never generates or stores it") — a manual bootstrap
// step with no corresponding security benefit now that orgdb is a store
// this same process already owns and controls directly. Safe for concurrent
// callers: a UNIQUE(namespace) constraint means at most one Create ever
// wins; a loser's error is treated as "someone else just created it" and
// retried once as a Get, not surfaced as a startup failure.
func EnsureSigningKey(ctx context.Context, store *orgdb.Store, namespace string) ([]byte, error) {
	if existing, err := store.GetSigningKeyByNamespace(ctx, namespace); err == nil {
		return decodeSigningKey(existing.KeyMaterial)
	} else if err != orgdb.ErrNotFound {
		return nil, fmt.Errorf("check for signing key: %w", err)
	}

	raw := make([]byte, signingKeyByteLength)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	if _, err := store.CreateSigningKey(ctx, orgdb.SigningKey{Namespace: namespace, KeyMaterial: encoded}); err != nil {
		// Another process (a second API replica starting concurrently) may
		// have just won the same race — re-fetch rather than fail startup
		// over what's actually a benign, expected outcome.
		if existing, getErr := store.GetSigningKeyByNamespace(ctx, namespace); getErr == nil {
			return decodeSigningKey(existing.KeyMaterial)
		}
		return nil, fmt.Errorf("create signing key: %w", err)
	}
	return raw, nil
}

func decodeSigningKey(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode stored signing key: %w", err)
	}
	return raw, nil
}
