package reconcile

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/types"
)

// secretType returns s.Type, defaulting to "Opaque" — matching `kubectl
// create secret generic`'s default.
func secretType(s *types.SecretSpec) string {
	if s.Type == "" {
		return "Opaque"
	}
	return s.Type
}

// resolveSecretKeys resolves each of s.Keys from the process environment,
// keyed by SecretKeyRef.Key in the result (not SecretKeyRef.Env — those
// differ when a key entry renames, e.g. {env: PORTAINER_PASSWORD, key:
// password}). A missing environment variable is a hard error naming every
// missing key — the deliberate opposite of a previous hand-rolled CI
// workflow's "silently skip if unset" behavior, which let a real deploy go
// out with secrets nobody had actually set. An empty-but-set value
// (KEY="") is accepted. Two entries producing the same output Key is also
// a hard error — otherwise one would silently clobber the other.
func resolveSecretKeys(s *types.SecretSpec) (map[string]string, error) {
	resolved := make(map[string]string, len(s.Keys))
	var missing []string
	for _, k := range s.Keys {
		v, ok := os.LookupEnv(k.Env)
		if !ok {
			missing = append(missing, k.Env)
			continue
		}
		if _, dup := resolved[k.Key]; dup {
			return nil, fmt.Errorf("secret resource: key %q is produced by more than one entry", k.Key)
		}
		resolved[k.Key] = v
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("secret resource: missing required environment variable(s): %s", strings.Join(missing, ", "))
	}
	return resolved, nil
}

// secretConfigHash is a content hash used to detect config drift for a
// Secret resource, the same role helmConfigHash plays for Helm. Takes the
// already-resolved key/value map (not just SecretSpec.Keys) so a changed
// environment *value* is drift too, not just a changed keys: list.
// Deterministic regardless of map iteration order. Pure — table-testable.
func secretConfigHash(s *types.SecretSpec, resolved map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "namespace=%s\n", s.Namespace)
	fmt.Fprintf(&b, "type=%s\n", secretType(s))
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "data.%s=%s\n", k, resolved[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:])
}

// secretManifest is the minimal shape needed to render a v1/Secret. Uses
// data (base64-encoded), NOT stringData: stringData is a write-only,
// additive convenience the API server merges into data at admission time,
// but a subsequent apply that stops mentioning a key in stringData never
// removes that key's already-persisted entry in data — confirmed
// empirically against a real cluster (managedFields correctly released
// ownership of the dropped stringData key, but the live object's data
// field kept the stale value regardless). data is the actual
// server-side-apply-tracked field, so it's the only field that gives
// correct per-key prune-by-omission when a key: is dropped from a Secret
// resource's keys: list.
type secretManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace,omitempty"`
	} `yaml:"metadata"`
	Type string            `yaml:"type,omitempty"`
	Data map[string]string `yaml:"data"`
}

// renderSecretManifest resolves s.Keys and marshals a v1/Secret manifest.
// Returns the resolved (plaintext) map alongside the manifest bytes so the
// caller can feed it straight to secretConfigHash without re-resolving —
// the hash is computed over plaintext values, independent of the
// base64 encoding used on the wire.
func renderSecretManifest(name string, s *types.SecretSpec) ([]byte, map[string]string, error) {
	resolved, err := resolveSecretKeys(s)
	if err != nil {
		return nil, nil, err
	}

	encoded := make(map[string]string, len(resolved))
	for k, v := range resolved {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}

	manifest := secretManifest{
		APIVersion: "v1",
		Kind:       "Secret",
		Type:       secretType(s),
		Data:       encoded,
	}
	manifest.Metadata.Name = name
	manifest.Metadata.Namespace = s.Namespace

	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal secret manifest: %w", err)
	}
	return data, resolved, nil
}
