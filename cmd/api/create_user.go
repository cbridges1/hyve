package api

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	hyveapi "github.com/cbridges1/hyve/internal/api"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/migrate"
	"github.com/cbridges1/hyve/internal/orgdb"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var (
	createUserRole             string
	createUserNamespace        string
	createUserEnvironment      string
	createUserServiceAccount   string
	createUserServiceAccountNS string
	createUserPassword         string
	createUserBindingName      string
	createUserApply            bool
	createUserKubeconfig       string
	createUserDBDriver         string
	createUserDBDSN            string
)

var createUserCmd = &cobra.Command{
	Use:   "create-user <username>",
	Short: "Bcrypt-hash a password and write a new local API user's binding + credentials Secret",
	Long: `Bcrypt-hashes a password (prompted interactively, or via --password for
scripting) and writes the user's access grant directly to hyve-api's own
organization/environment/RBAC datastore (internal/orgdb — Postgres or
SQLite, matching --db/--db-dsn to whatever that same install's hyve-api
process uses) — this is a real database write, not YAML you can pipe to
'kubectl apply -f -', since a binding is a Postgres/SQLite row now, not a
Kubernetes object (see HYVE-ORGANIZATION-MODEL-PROPOSAL.md, nexus-config/
docs, and this command's own predecessor before
HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 4).

The paired credentials Secret (holding the bcrypt hash itself — never the
binding row) is still a real Kubernetes object, and still defaults to
printing its YAML to stdout for 'kubectl apply -f -', matching this
command's original, kubectl-manageable design for that one remaining half.
Pass --apply to create it directly instead, using the same kubeconfig
resolution as a bare kubectl invocation ($KUBECONFIG, else ~/.kube/config
— override with --kubeconfig). Either way, the binding write to --db-dsn
always happens — there's no "print it and apply later" option for that
part.

Safe to re-run: an already-existing binding for the same identity+scope is
replaced (delete then recreate) rather than erroring, and an
already-existing Secret is updated in place under --apply.

Example:
  hyve cluster-config api create-user cedric --role admin --db-dsn /data/orgdb.sqlite | kubectl apply -f -
  hyve cluster-config api create-user cedric --role admin --db-dsn /data/orgdb.sqlite --apply`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runCreateUser(args[0])
	},
}

func init() {
	createUserCmd.Flags().StringVar(&createUserRole, "role", "", "Role: admin, read-only, superadmin, or custom (required)")
	createUserCmd.Flags().StringVar(&createUserNamespace, "namespace", "hyve-system", "Namespace this binding is scoped to (the actual isolation boundary — see internal/orgdb.Binding) and the credentials Secret is created in. For --role superadmin, this must match whatever namespace the target install's own hyve-api --namespace flag uses (its control-plane namespace) — that's the one place login looks for a superadmin binding; this command has no live Server to read that value from, so it isn't enforced here the way POST /accounts enforces it server-side.")
	createUserCmd.Flags().StringVar(&createUserEnvironment, "env", "", "Environment within --namespace's own Organization, if one is registered (omit to default to that organization's own environment, when it has exactly one — see internal/api.resolveResourceEnvironment's identical rule). Ignored when --namespace has no registered Organization at all, or for --role superadmin.")
	createUserCmd.Flags().StringVar(&createUserServiceAccount, "service-account", "", "ServiceAccount this binding's grant maps to (defaults per --role: hyve-access-admin / hyve-access-readonly; required for --role custom)")
	createUserCmd.Flags().StringVar(&createUserServiceAccountNS, "service-account-namespace", "hyve-system", "Namespace of --service-account")
	createUserCmd.Flags().StringVar(&createUserPassword, "password", "", "Password (scripting only — omit to be prompted interactively without echo)")
	createUserCmd.Flags().StringVar(&createUserBindingName, "binding-name", "", "Deprecated, ignored — a binding's identity is always the username now; kept only so an old invocation setting this doesn't hard-fail. Will be removed.")
	createUserCmd.Flags().BoolVar(&createUserApply, "apply", false, "Create the credentials Secret directly instead of printing its YAML to stdout — the binding write to --db-dsn happens either way")
	createUserCmd.Flags().StringVar(&createUserKubeconfig, "kubeconfig", "", "Kubeconfig path for --apply (default: $KUBECONFIG, else ~/.kube/config, same as kubectl)")
	createUserCmd.Flags().StringVar(&createUserDBDriver, "db", "sqlite", "Backend for hyve-api's own organization datastore this binding is written to — must match that install's own hyve-api --db (see internal/orgdb)")
	createUserCmd.Flags().StringVar(&createUserDBDSN, "db-dsn", "/data/orgdb.sqlite", "Data source name for --db — must point at the same database the target install's own hyve-api uses")
}

func runCreateUser(username string) {
	role := createUserRole
	switch role {
	case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleReadOnly, hyvev1alpha1.RoleCustom, hyvev1alpha1.RoleSuperadmin:
	case "":
		log.Fatal("--role is required (admin, read-only, superadmin, or custom)")
	default:
		log.Fatalf("--role must be admin, read-only, superadmin, or custom, got %q", role)
	}

	saName := createUserServiceAccount
	if saName == "" {
		switch role {
		case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin, hyvev1alpha1.RoleReadOnly:
			saName = orgdb.ServiceAccountNameForRole(role)
		case hyvev1alpha1.RoleCustom:
			log.Fatal("--service-account is required for --role custom")
		}
	}

	password := createUserPassword
	if password == "" {
		var err error
		password, err = promptPassword()
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
	}
	if password == "" {
		log.Fatal("password must not be empty")
	}

	hash, err := hyveapi.HashPassword(password)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	// See --namespace's own flag help: for --role superadmin, the caller is
	// responsible for passing the target install's actual control-plane
	// namespace here — this command has no live Server to resolve or
	// enforce that value the way POST /accounts does.
	bindingNamespace := createUserNamespace
	secret := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: hyveapi.UserCredentialsSecretName(username), Namespace: bindingNamespace},
		StringData: map[string]string{"password-hash": hash},
	}

	writeBindingToStore(username, role, bindingNamespace, saName)

	if createUserApply {
		applySecret(secret)
		return
	}
	printYAML(secret)
}

// writeBindingToStore is the actual access-grant write — a
// database.orgdb.Store CreateBinding call, replacing this command's
// former "print a HyveAccessBinding YAML manifest" behavior outright (see
// this file's own package doc comment for why: a binding is a Postgres/
// SQLite row now, not something 'kubectl apply -f -' can create). Resolves
// organization/environment the same way handleCreateAccount does: if
// --namespace has a registered Organization, scope to it (via --env or
// that organization's own single environment); otherwise nil-scope,
// exactly like a superadmin's — the self-hosted, no-Organization-
// registered case.
func writeBindingToStore(username, role, namespace, serviceAccountName string) {
	store, err := orgdb.Open(createUserDBDriver, createUserDBDSN)
	if err != nil {
		log.Fatalf("Failed to open --db=%s organization datastore at %q: %v", createUserDBDriver, createUserDBDSN, err)
	}
	defer store.Close()

	ctx := context.Background()

	b := orgdb.Binding{
		Namespace:               namespace,
		SubjectType:             orgdb.SubjectTypeLocal,
		Identity:                username,
		Role:                    role,
		ServiceAccountName:      serviceAccountName,
		ServiceAccountNamespace: createUserServiceAccountNS,
	}

	if role != hyvev1alpha1.RoleSuperadmin {
		org, orgErr := store.GetOrganizationByName(ctx, namespace)
		switch {
		case orgErr == nil:
			envs, envErr := store.ListEnvironments(ctx, org.ID)
			if envErr != nil {
				log.Fatalf("Failed to list environments for organization %q: %v", org.Name, envErr)
			}
			env, envErr := resolveEnvironment(envs, createUserEnvironment, org.Name)
			if envErr != nil {
				log.Fatal(envErr)
			}
			b.OrganizationID = &org.ID
			b.EnvironmentID = &env.ID
		case orgErr == orgdb.ErrNotFound:
			// No Organization registered for this namespace — the
			// self-hosted, single-tenant fallback (see
			// internal/api/identity.go's organizationIDForNamespace doc
			// comment for the identical rule POST /accounts follows).
			if createUserEnvironment != "" {
				log.Fatalf("--env %q given, but no organization is registered for namespace %q — nothing to resolve it against", createUserEnvironment, namespace)
			}
		default:
			log.Fatalf("Failed to resolve organization for namespace %q: %v", namespace, orgErr)
		}
	}

	// Safe to re-run: replace an already-existing binding for this exact
	// identity+scope rather than erroring — the same "an already-existing
	// object is updated in place" contract this command has always had,
	// now expressed as delete-then-recreate rather than a Kubernetes
	// Update, since a binding's own identity fields (namespace, org,
	// environment) are exactly what a re-run might legitimately be
	// changing (e.g. moving a user from read-only to admin).
	if existing, findErr := store.FindBindingBySubject(ctx, namespace, orgdb.SubjectTypeLocal, username); findErr == nil {
		if delErr := store.DeleteBinding(ctx, existing.ID); delErr != nil {
			log.Fatalf("Failed to replace existing binding for %q: %v", username, delErr)
		}
	} else if findErr != orgdb.ErrNotFound {
		log.Fatalf("Failed to check existing binding for %q: %v", username, findErr)
	}

	if _, err := store.CreateBinding(ctx, b); err != nil {
		log.Fatalf("Failed to write binding for %q: %v", username, err)
	}
	fmt.Printf("✅ Binding for %q (role=%s) written to %s database at %s\n", username, role, createUserDBDriver, createUserDBDSN)
}

// resolveEnvironment mirrors internal/api.resolveResourceEnvironment's
// resolution rule exactly (explicit name, or the organization's own single
// environment, or a clear error) — duplicated here in miniature rather
// than exported from internal/api, since that package's own version is a
// Server method reaching into request-scoped state (r.URL.Query()) this
// CLI command has no equivalent of.
func resolveEnvironment(envs []orgdb.Environment, requested, orgName string) (orgdb.Environment, error) {
	if requested != "" {
		for _, e := range envs {
			if e.Name == requested {
				return e, nil
			}
		}
		return orgdb.Environment{}, fmt.Errorf("environment %q does not exist for organization %q", requested, orgName)
	}
	switch len(envs) {
	case 1:
		return envs[0], nil
	case 0:
		return orgdb.Environment{}, fmt.Errorf("organization %q has no environments", orgName)
	default:
		names := make([]string, len(envs))
		for i, e := range envs {
			names[i] = e.Name
		}
		return orgdb.Environment{}, fmt.Errorf("organization %q has multiple environments (%s) — specify --env", orgName, strings.Join(names, ", "))
	}
}

// applySecret creates the credentials Secret directly against the cluster
// (--apply), rather than printing YAML for a separate 'kubectl apply -f -'.
// Uses migrate.BuildClient's same kubeconfig resolution
// (clientcmd.BuildConfigFromFlags with an empty path falling through to
// $KUBECONFIG/~/.kube/config, exactly like a bare kubectl invocation —
// confirmed against cmd/migrate_resolve.go's own identical precedent) so
// this behaves the same as the 'kubectl apply -f -' it's replacing.
func applySecret(secret *corev1.Secret) {
	c, err := migrate.BuildClient(createUserKubeconfig)
	if err != nil {
		log.Fatalf("Failed to build Kubernetes client: %v", err)
	}
	ctx := context.Background()

	if err := createOrUpdate(ctx, c, secret); err != nil {
		log.Fatalf("Failed to apply Secret %s/%s: %v", secret.Namespace, secret.Name, err)
	}
	fmt.Printf("✅ Secret %s/%s applied\n", secret.Namespace, secret.Name)
}

// createOrUpdate creates obj, or — if it already exists — fetches the
// current resourceVersion and updates it in place. client.Object's own
// Create/Update calls mutate obj's ResourceVersion/UID on success, so a
// caller inspecting obj afterward sees the live server state either way.
func createOrUpdate(ctx context.Context, c client.Client, obj client.Object) error {
	err := c.Create(ctx, obj)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}

	existing := obj.DeepCopyObject().(client.Object)
	key := k8stypes.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	if getErr := c.Get(ctx, key, existing); getErr != nil {
		return fmt.Errorf("fetch existing object to update: %w", getErr)
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return c.Update(ctx, obj)
}

func printYAML(obj interface{}) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		log.Fatalf("Failed to marshal YAML: %v", err)
	}
	fmt.Print(string(data))
}

// promptPassword reads a password from the terminal without echoing it —
// falls back to a plain (echoed) line read when stdin isn't a real
// terminal (e.g. piped input in a test), since term.ReadPassword requires
// an actual TTY file descriptor.
func promptPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Password: ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
