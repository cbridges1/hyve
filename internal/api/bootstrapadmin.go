package api

import (
	"context"
	"fmt"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"
)

// SeedBootstrapAdmin creates the install's first superadmin from operator-
// supplied startup values (cmd/api/run.go's --bootstrap-admin-* flags,
// typically fed by Helm values and a mounted Secret) — but only while
// namespace (the control plane's own) has no superadmin binding at all.
// Same "seed once, the store owns it after that" idiom as
// SeedEmailSettings/EnsureSigningKey: once any superadmin exists, a restart
// never recreates, renames, or resets a password. There is deliberately no
// HTTP surface for this — it runs inside the server process at startup,
// the same write `hyve cluster-config api create-user --role superadmin`
// makes. username == "" is a no-op. created reports whether a binding was
// written.
func SeedBootstrapAdmin(ctx context.Context, store *orgdb.Store, namespace, username, password string) (created bool, err error) {
	if username == "" {
		return false, nil
	}
	bindings, err := store.ListBindingsForScope(ctx, namespace)
	if err != nil {
		return false, fmt.Errorf("check existing superadmins: %w", err)
	}
	for _, b := range bindings {
		if b.Role == hyvev1alpha1.RoleSuperadmin {
			return false, nil
		}
	}
	// Checked only once an admin actually needs creating, so a password
	// Secret removed after the first start never breaks a later one.
	if password == "" {
		return false, fmt.Errorf("no superadmin exists yet and the bootstrap admin password is empty")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return false, fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	if _, err := store.CreateBinding(ctx, orgdb.Binding{
		Namespace:               namespace,
		SubjectType:             orgdb.SubjectTypeLocal,
		Identity:                username,
		Role:                    hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName:      orgdb.ServiceAccountNameForRole(hyvev1alpha1.RoleSuperadmin),
		ServiceAccountNamespace: namespace,
		PasswordHash:            &hash,
	}); err != nil {
		return false, fmt.Errorf("create bootstrap admin: %w", err)
	}
	return true, nil
}
