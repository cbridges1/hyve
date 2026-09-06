package api

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	hyveapi "github.com/cbridges1/hyve/internal/api"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

var (
	createUserRole             string
	createUserNamespace        string
	createUserServiceAccount   string
	createUserServiceAccountNS string
	createUserPassword         string
	createUserBindingName      string
)

var createUserCmd = &cobra.Command{
	Use:   "create-user <username>",
	Short: "Emit the Secret + HyveAccessBinding YAML for a new local API user",
	Long: `Bcrypt-hashes a password (prompted interactively, or via --password for
scripting) and prints the paired Kubernetes Secret + HyveAccessBinding YAML
for a new local user to stdout. Apply it yourself with 'kubectl apply -f -'
— this command never talks to the Kubernetes API, keeping user
provisioning kubectl-manageable and GitOps-friendly like every other piece
of static config in this plan.

Example:
  hyve cluster-config api create-user cedric --role admin | kubectl apply -f -`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runCreateUser(args[0])
	},
}

func init() {
	createUserCmd.Flags().StringVar(&createUserRole, "role", "", "Role: admin, read-only, or custom (required)")
	createUserCmd.Flags().StringVar(&createUserNamespace, "namespace", "hyve-system", "Namespace the credentials Secret is created in")
	createUserCmd.Flags().StringVar(&createUserServiceAccount, "service-account", "", "ServiceAccount TokenRequest mints against for this user (defaults per --role: hyve-access-admin / hyve-access-readonly; required for --role custom)")
	createUserCmd.Flags().StringVar(&createUserServiceAccountNS, "service-account-namespace", "hyve-system", "Namespace of --service-account")
	createUserCmd.Flags().StringVar(&createUserPassword, "password", "", "Password (scripting only — omit to be prompted interactively without echo)")
	createUserCmd.Flags().StringVar(&createUserBindingName, "binding-name", "", "HyveAccessBinding name (defaults to the username)")
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
		case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin:
			// superadmin's own cross-namespace endpoints (POST/GET
			// /environments, the host-cluster kubeconfig path) are gated by
			// HyveAccessBindingSpec.Role alone, not by ServiceAccountRef —
			// this SA only matters if a superadmin also fetches an ordinary
			// GET /api/kubeconfig for some tenant's cluster, so reusing
			// hyve-access-admin (already created in this namespace) is fine
			// rather than needing a role-specific ServiceAccount of its own.
			saName = "hyve-access-admin"
		case hyvev1alpha1.RoleReadOnly:
			saName = "hyve-access-readonly"
		case hyvev1alpha1.RoleCustom:
			log.Fatal("--service-account is required for --role custom")
		}
	}

	bindingName := createUserBindingName
	if bindingName == "" {
		bindingName = username
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

	secret := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: hyveapi.UserCredentialsSecretName(bindingName), Namespace: createUserNamespace},
		StringData: map[string]string{"password-hash": hash},
	}

	binding := &hyvev1alpha1.HyveAccessBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "hyve.io/v1alpha1", Kind: "HyveAccessBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: bindingName, Namespace: createUserNamespace},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject:           hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: username},
			Role:              role,
			ServiceAccountRef: hyvev1alpha1.ServiceAccountRef{Name: saName, Namespace: createUserServiceAccountNS},
		},
	}

	printYAML(secret)
	fmt.Println("---")
	printYAML(binding)
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
