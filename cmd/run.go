package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"hyve/internal/kubeconfig"
	"hyve/internal/repository"
)

var runCmd = &cobra.Command{
	Use:   "run [command] [args...]",
	Short: "Run a command with kubeconfig set for specified cluster",
	Long: `Execute a command with the kubeconfig environment variable set to a specific cluster.
If no cluster is specified with --cluster, uses the current repository's default or prompts for selection.

Examples:
  # Run kubectl with production cluster (individual args)
  ./hyve run --cluster production kubectl get nodes

  # Run kubectl with flags using string mode
  ./hyve run --cluster production --string "kubectl -n=default get pods"

  # Interactive cluster selection
  ./hyve run kubectl get services

  # Complex commands with string mode
  ./hyve run --cluster production --string "kubectl get pods | grep nginx"

  # Run any command with staging cluster
  ./hyve run --cluster staging kubectl get pods -o wide`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		clusterName, _ := cmd.Flags().GetString("cluster")
		commandString, _ := cmd.Flags().GetString("string")

		if commandString != "" {
			if len(args) > 0 {
				log.Fatal("Cannot specify both --string flag and command arguments")
			}
			cmdArgs := parseCommandString(commandString)
			if len(cmdArgs) == 0 {
				log.Fatal("Empty command string provided")
			}
			runWithKubeconfig(clusterName, cmdArgs, commandString)
		} else {
			if len(args) < 1 {
				log.Fatal("Must specify either command arguments or --string flag")
			}
			runWithKubeconfig(clusterName, args, "")
		}
	},
}

func init() {
	runCmd.Flags().StringP("cluster", "c", "", "Cluster name to use for kubeconfig")
	runCmd.Flags().StringP("string", "s", "", "Run command from string (supports complex commands with flags and pipes)")
}

// setupTempKubeconfig resolves the cluster name, retrieves its kubeconfig, writes it
// to a temporary file, and returns the file path plus a cleanup function.
func setupTempKubeconfig(clusterName string) (kubeconfigPath string, cleanup func()) {
	repoManager, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	currentRepo, err := repoManager.GetCurrentRepository()
	if err != nil {
		log.Fatalf("No current repository configured. Please add a repository first: %v", err)
	}

	manager, err := kubeconfig.NewManager(currentRepo.Name)
	if err != nil {
		log.Fatalf("Failed to initialize kubeconfig manager: %v", err)
	}

	if clusterName == "" {
		kubeconfigs, err := manager.ListKubeconfigs()
		if err != nil {
			log.Fatalf("Failed to list kubeconfigs: %v", err)
		}
		if len(kubeconfigs) == 0 {
			log.Fatal("No kubeconfigs found. Run 'hyve kubeconfig sync' first.")
		}
		if len(kubeconfigs) == 1 {
			clusterName = kubeconfigs[0].ClusterName
			log.Printf("Using cluster: %s\n", clusterName)
		} else {
			fmt.Println("Available clusters:")
			for i, kc := range kubeconfigs {
				log.Printf("  %d. %s\n", i+1, kc.ClusterName)
			}
			log.Fatal("Please specify a cluster with --cluster flag")
		}
	}

	kc, err := manager.GetKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("Failed to get kubeconfig for cluster '%s': %v", clusterName, err)
	}
	kubeconfigData, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig data: %v", err)
	}

	tempDir := filepath.Join(HyveHome(), "temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	tempFile := filepath.Join(tempDir, fmt.Sprintf("kubeconfig-run-%s", kc.ClusterName))
	if err := os.WriteFile(tempFile, []byte(kubeconfigData), 0600); err != nil {
		log.Fatalf("Failed to write temporary kubeconfig: %v", err)
	}

	cleanup = func() {
		manager.Close()
		if err := os.Remove(tempFile); err != nil {
			log.Printf("Warning: Failed to clean up temporary kubeconfig: %v", err)
		}
	}
	return tempFile, cleanup
}

// runWithKubeconfig executes cmdArgs with KUBECONFIG pointing to the given cluster.
// displayCmd is shown in log output; pass "" to log the args slice instead.
func runWithKubeconfig(clusterName string, cmdArgs []string, displayCmd string) {
	kubeconfigPath, cleanup := setupTempKubeconfig(clusterName)
	defer cleanup()

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Derive cluster name from the temp file path for the log message
	clusterLabel := strings.TrimPrefix(filepath.Base(kubeconfigPath), "kubeconfig-run-")
	if displayCmd != "" {
		log.Printf("🚀 Running command with cluster '%s' kubeconfig...\n", clusterLabel)
		log.Printf("💡 Command: %s\n", displayCmd)
	} else {
		log.Printf("🚀 Running command with cluster '%s' kubeconfig...\n", clusterLabel)
		log.Printf("💡 Command: %s %v\n", cmdArgs[0], cmdArgs[1:])
	}
	fmt.Println()

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}
		log.Fatalf("Command failed: %v", err)
	}
}

// parseCommandString performs simple shell-like parsing of a command string.
func parseCommandString(commandString string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(commandString); i++ {
		char := commandString[i]
		switch {
		case !inQuote && (char == '"' || char == '\''):
			inQuote = true
			quoteChar = char
		case inQuote && char == quoteChar:
			inQuote = false
			quoteChar = 0
		case !inQuote && char == ' ':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		case !inQuote && char == '\\' && i+1 < len(commandString):
			i++
			current.WriteByte(commandString[i])
		default:
			current.WriteByte(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
