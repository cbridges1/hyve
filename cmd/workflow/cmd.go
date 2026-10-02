package workflow

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/workflow"
	"github.com/cbridges1/hyve/internal/workflowref"
)

// Cmd is the root workflow command exposed to the parent.
var Cmd = workflowCmd

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage workflows",
	Long: `Manage workflows for automated task execution.
Workflows are defined in YAML files stored in the 'workflows' directory of your repository.`,
}

var workflowCreateCmd = &cobra.Command{
	Use:   "create [workflow-name]",
	Short: "Create a new workflow",
	Long: `Create a new workflow from a template or interactive input.
If no name is provided, you'll be prompted for workflow details.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		template, _ := cmd.Flags().GetBool("template")
		description, _ := cmd.Flags().GetString("description")
		file, _ := cmd.Flags().GetString("file")

		if sess, ok := shared.UseClusterMode(); ok {
			client := shared.NewAPIClient(sess)
			if file != "" {
				createWorkflowFromFileAPI(client, file)
			} else if template || len(args) > 0 {
				name := ""
				if len(args) > 0 {
					name = args[0]
				}
				createWorkflowTemplateAPI(client, name, description)
			} else {
				log.Fatal("Must specify either workflow name with --template, or use --file to create from existing file")
			}
			return
		}

		if file != "" {
			createWorkflowFromFile(file)
		} else if template || len(args) > 0 {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			createWorkflowTemplate(name, description)
		} else {
			log.Fatal("Must specify either workflow name with --template, or use --file to create from existing file")
		}
	},
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workflows",
	Long:  "List all available workflows in the current repository.",
	Run: func(cmd *cobra.Command, args []string) {
		if sess, ok := shared.UseClusterMode(); ok {
			listWorkflowsAPI(shared.NewAPIClient(sess))
			return
		}
		listWorkflows()
	},
}

var workflowShowCmd = &cobra.Command{
	Use:   "show [workflow-name]",
	Short: "Show workflow details",
	Long:  "Display detailed information about a specific workflow.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if sess, ok := shared.UseClusterMode(); ok {
			showWorkflowAPI(shared.NewAPIClient(sess), args[0])
			return
		}
		showWorkflow(args[0])
	},
}

var workflowRunCmd = &cobra.Command{
	Use:   "run [workflow-name]",
	Short: "Run a workflow",
	Long: `Execute a workflow on a cluster.
If no cluster is specified, the workflow will run without cluster context (local commands only).

Required workflow inputs that are not already in the environment must be supplied with --set:

  hyve workflow run provision-network --set HYVE_CLUSTER_NAME=my-cluster --set HYVE_CLUSTER_REGION=eastus`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, _ := cmd.Flags().GetString("cluster")
		showOutput, _ := cmd.Flags().GetBool("output")
		showLogs, _ := cmd.Flags().GetBool("logs")
		setStrs, _ := cmd.Flags().GetStringArray("set")
		pathFlag, _ := cmd.Flags().GetString("path")

		setVars, err := parseSetVars(setStrs)
		if err != nil {
			log.Fatalf("Invalid --set flag: %v", err)
		}

		if sess, ok := shared.UseClusterMode(); ok {
			// Cluster mode has no separate "step logs" stream to gate behind
			// --output specifically — a WorkflowRun's status.output is one
			// combined capture. --logs (default true, matching local mode's
			// own default-visible behavior) is the flag that actually
			// determines whether a caller sees anything at all; --output
			// alone would leave `hyve workflow run` silent by default.
			runWorkflowClusterMode(shared.NewAPIClient(sess), args[0], pathFlag, cluster, showLogs, setVars)
			return
		}

		runWorkflowByRef(args[0], pathFlag, cluster, showLogs, showOutput, setVars)
	},
}

var workflowDeleteCmd = &cobra.Command{
	Use:   "delete [workflow-name]",
	Short: "Delete a workflow",
	Long:  "Remove a workflow definition from the repository.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		if sess, ok := shared.UseClusterMode(); ok {
			deleteWorkflowAPI(shared.NewAPIClient(sess), args[0], force)
			return
		}
		deleteWorkflow(args[0], force)
	},
}

var workflowValidateCmd = &cobra.Command{
	Use:   "validate [workflow-name]",
	Short: "Validate a workflow",
	Long:  "Validate the syntax and structure of a workflow definition. Local mode only — same reasoning as `hyve workflow run`.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve workflow validate` is local-mode only — it checks the definition against your local checkout, which has no equivalent in cluster mode. Run it against a local checkout instead.")
		}
		validateWorkflow(args[0])
	},
}

func init() {
	workflowCreateCmd.Flags().BoolP("template", "t", false, "Create from default template")
	workflowCreateCmd.Flags().StringP("description", "d", "", "Workflow description")
	workflowCreateCmd.Flags().StringP("file", "f", "", "Create workflow from existing YAML file")

	workflowRunCmd.Flags().StringP("cluster", "c", "", "Cluster to run workflow on")
	workflowRunCmd.Flags().BoolP("logs", "l", true, "Show execution logs")
	workflowRunCmd.Flags().BoolP("output", "o", false, "Show step outputs")
	workflowRunCmd.Flags().StringArray("set", nil, "Set a workflow input variable: KEY=VALUE (repeatable)")
	workflowRunCmd.Flags().String("path", "", "Override/supply the source's path component (remote sources only)")

	workflowDeleteCmd.Flags().BoolP("force", "f", false, "Delete without confirmation")

	workflowCmd.AddCommand(workflowCreateCmd)
	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowShowCmd)
	workflowCmd.AddCommand(workflowRunCmd)
	workflowCmd.AddCommand(workflowDeleteCmd)
	workflowCmd.AddCommand(workflowValidateCmd)
}

func getWorkflowLocalPath() string {
	return shared.GetLocalPath()
}

func createWorkflowTemplate(name, description string) {
	if name == "" {
		log.Fatal("Workflow name is required when using --template")
	}

	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	wf := workflow.CreateWorkflowTemplate(name, description)

	if err := manager.CreateWorkflow(wf); err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}

	log.Printf("✅ Created workflow template '%s'", name)
	log.Printf("📁 Location: %s/%s.yaml", manager.GetWorkflowsPath(), name)
	log.Printf("🔧 Edit the file to customize your workflow")
}

func createWorkflowFromFile(filePath string) {
	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read file '%s': %v", filePath, err)
	}

	var wf workflow.Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		log.Fatalf("Failed to parse workflow file: %v", err)
	}

	if err := manager.CreateWorkflow(&wf); err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}

	log.Printf("✅ Created workflow '%s' from file", wf.Metadata.Name)
	log.Printf("📁 Location: %s/%s.yaml", manager.GetWorkflowsPath(), wf.Metadata.Name)
}

func listWorkflows() {
	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	workflows, err := manager.ListWorkflows()
	if err != nil {
		log.Fatalf("Failed to list workflows: %v", err)
	}

	// Best-effort; a missing/unreadable hyve.lock just means no
	// git-referenced workflows to add to the listing below — this is
	// local mode's equivalent of cluster mode's WorkflowRefStatus merge
	// (internal/api/workflows.go's handleListWorkflows), using the
	// mechanism local mode already has instead of a CRD it has no use for.
	lf, _ := module.LoadLockFile(getWorkflowLocalPath())

	if len(workflows) == 0 && (lf == nil || len(lf.Workflows) == 0) {
		log.Println("No workflows found in repository")
		log.Printf("💡 Create a workflow with: hyve workflow create --template my-workflow")
		return
	}

	total := len(workflows)
	if lf != nil {
		total += len(lf.Workflows)
	}
	log.Printf("📋 Workflows in repository (%d):\n", total)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tJOBS\tCREATED\tSOURCE")

	for _, wf := range workflows {
		created := wf.Metadata.Created.Format("2006-01-02")
		if wf.Metadata.Created.IsZero() {
			created = "unknown"
		}

		description := wf.Metadata.Description
		if len(description) > 50 {
			description = description[:47] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			wf.Metadata.Name,
			description,
			len(wf.Spec.Jobs),
			created,
			"")
	}
	if lf != nil {
		for _, locked := range lf.Workflows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", locked.Name, "", "", "", locked.Source)
		}
	}

	w.Flush()

	log.Printf("\n💡 Commands:")
	log.Printf("   hyve workflow show <name>     # Show workflow details")
	log.Printf("   hyve workflow run <name>      # Run workflow")
	log.Printf("   hyve workflow delete <name>   # Delete workflow")
}

func showWorkflow(name string) {
	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	wf, err := manager.GetWorkflow(name)
	if err != nil {
		showRemoteWorkflowFromLock(name)
		return
	}

	log.Printf("📋 Workflow: %s", wf.Metadata.Name)
	if wf.Metadata.Description != "" {
		log.Printf("📝 Description: %s", wf.Metadata.Description)
	}
	log.Printf("📅 Created: %s", wf.Metadata.Created.Format("2006-01-02 15:04:05"))

	if len(wf.Metadata.Labels) > 0 {
		log.Printf("🏷️  Labels:")
		for key, value := range wf.Metadata.Labels {
			log.Printf("   %s: %s", key, value)
		}
	}

	if len(wf.Spec.Inputs) > 0 {
		log.Printf("📥 Required Inputs:")
		for _, input := range wf.Spec.Inputs {
			if input.Description != "" {
				log.Printf("   %s — %s", input.Name, input.Description)
			} else {
				log.Printf("   %s", input.Name)
			}
			if input.Default != "" {
				log.Printf("      (default: %s)", input.Default)
			}
		}
		log.Printf("   💡 Supply with: hyve workflow run %s --set KEY=VALUE", wf.Metadata.Name)
	}

	if len(wf.Spec.Env) > 0 {
		log.Printf("🌍 Environment Variables:")
		for key, value := range wf.Spec.Env {
			log.Printf("   %s: %s", key, value)
		}
	}

	log.Printf("\n🚀 Jobs (%d):", len(wf.Spec.Jobs))
	for i, job := range wf.Spec.Jobs {
		log.Printf("\n  %d. %s", i+1, job.Name)
		if job.Description != "" {
			log.Printf("     📝 %s", job.Description)
		}
		if len(job.DependsOn) > 0 {
			log.Printf("     🔗 Depends on: %s", strings.Join(job.DependsOn, ", "))
		}
		if job.Cluster != "" {
			log.Printf("     🎯 Cluster: %s", job.Cluster)
		}
		if job.If != "" {
			log.Printf("     ❓ Condition: %s", job.If)
		}

		log.Printf("     📋 Steps (%d):", len(job.Steps))
		for j, step := range job.Steps {
			log.Printf("       %d. %s", j+1, step.Name)
			if step.Command != "" {
				log.Printf("          🔧 Command: %s", step.Command)
			}
			if step.Script != "" {
				log.Printf("          📜 Script: %s", step.Script)
			}
			if step.Action != "" {
				log.Printf("          ⚡ Action: %s", step.Action)
			}
		}
	}

	log.Printf("\n💡 Run with: hyve workflow run %s", name)
}

// showRemoteWorkflowFromLock is showWorkflow's fallback once the local
// workflows/ dir has no such name — mirrors runWorkflowByRef's own
// hyve.lock-by-Name lookup (cmd/workflow/run_remote.go) exactly, but
// displays the resolved content instead of executing it.
func showRemoteWorkflowFromLock(name string) {
	repoPath := getWorkflowLocalPath()
	lf, err := module.LoadLockFile(repoPath)
	if err != nil {
		log.Fatalf("Failed to get workflow: not found locally, and failed to load hyve.lock: %v", err)
	}
	matches := lf.FindLockedWorkflowsByName(name)
	switch len(matches) {
	case 0:
		log.Fatalf("workflow %q not found locally or in hyve.lock — run `hyve workflow list` or `hyve workflow install`", name)
	case 1:
		full := matches[0].Source
		if matches[0].Version != "" {
			full += "@" + matches[0].Version
		}
		files, err := workflowref.Resolve(full, "", lf, "")
		if err != nil {
			log.Fatalf("Failed to resolve workflow %q: %v", full, err)
		}
		log.Printf("📋 Workflow: %s (git-referenced: %s)\n", name, full)
		log.Println(string(files[0].Data))
	default:
		var b strings.Builder
		for _, m := range matches {
			fmt.Fprintf(&b, "  %s@%s\n", m.Source, m.Version)
		}
		log.Fatalf("workflow name %q is ambiguous across %d locked sources — run with the full source string instead:\n%s", name, len(matches), b.String())
	}
}

func runWorkflow(name, cluster string, showLogs, showOutput bool, setVars map[string]string) {
	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	executor, err := workflow.NewExecutor(manager, cluster)
	if err != nil {
		log.Fatalf("Failed to create workflow executor: %v", err)
	}
	defer executor.Close()
	executor.KubeconfigLocator = module.KubeconfigPathForCluster

	if len(setVars) > 0 {
		executor.InjectVars(setVars)
	}

	ctx := context.Background()

	log.Printf("🚀 Starting workflow '%s'", name)
	if cluster != "" {
		log.Printf("🎯 Target cluster: %s", cluster)
	} else {
		log.Printf("💻 Running locally (no cluster context)")
	}
	log.Println()

	execution, err := executor.RunWorkflow(ctx, name, cluster)
	if err != nil {
		log.Printf("❌ Workflow failed: %v", err)
		if showLogs && execution != nil {
			printExecutionLogs(execution, showOutput)
		}
		os.Exit(1)
	}

	log.Printf("✅ Workflow '%s' completed successfully", name)
	log.Printf("⏱️  Duration: %v", execution.Duration)

	if showLogs {
		printExecutionLogs(execution, showOutput)
	}

	printExecutionSummary(execution)
}

// parseSetVars converts ["KEY=VALUE", ...] into a map.
func parseSetVars(strs []string) (map[string]string, error) {
	out := make(map[string]string, len(strs))
	for _, s := range strs {
		idx := strings.Index(s, "=")
		if idx < 1 {
			return nil, fmt.Errorf("%q is not in KEY=VALUE format", s)
		}
		out[s[:idx]] = s[idx+1:]
	}
	return out, nil
}

func deleteWorkflow(name string, force bool) {
	if !force {
		log.Printf("Are you sure you want to delete workflow '%s'? (y/N): ", name)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			log.Println("Delete cancelled")
			return
		}
	}

	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	if err := manager.DeleteWorkflow(name); err != nil {
		log.Fatalf("Failed to delete workflow: %v", err)
	}

	log.Printf("✅ Deleted workflow '%s'", name)
}

func validateWorkflow(name string) {
	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	wf, err := manager.GetWorkflow(name)
	if err != nil {
		log.Fatalf("Failed to get workflow: %v", err)
	}

	log.Printf("🔍 Validating workflow '%s'...\n", name)

	errors, warnings := workflow.Validate(wf)

	if len(errors) > 0 {
		log.Println("\n❌ Validation Failed")
		log.Println("\nErrors:")
		for _, err := range errors {
			log.Printf("  • %s", err)
		}
	}

	if len(warnings) > 0 {
		log.Println("\n⚠️  Warnings:")
		for _, warn := range warnings {
			log.Printf("  • %s", warn)
		}
	}

	if len(errors) == 0 {
		log.Println("\n✅ Workflow is valid")
		log.Printf("📋 Jobs: %d", len(wf.Spec.Jobs))
		totalSteps := 0
		for _, job := range wf.Spec.Jobs {
			totalSteps += len(job.Steps)
		}
		log.Printf("📋 Total steps: %d", totalSteps)

		if len(warnings) == 0 {
			log.Println("✨ No warnings")
		}
	} else {
		os.Exit(1)
	}
}

func printExecutionLogs(execution *workflow.WorkflowExecution, showOutput bool) {
	log.Println("\n📋 Execution Logs:")
	log.Println(strings.Repeat("=", 60))

	for _, logEntry := range execution.Logs {
		timestamp := logEntry.Timestamp.Format("15:04:05")
		prefix := fmt.Sprintf("[%s][%s]", timestamp, logEntry.Level)

		if logEntry.Job != "" {
			prefix += fmt.Sprintf("[%s]", logEntry.Job)
		}
		if logEntry.Step != "" {
			prefix += fmt.Sprintf("[%s]", logEntry.Step)
		}

		log.Printf("%s %s", prefix, logEntry.Message)
	}

	if showOutput {
		log.Println("\n📤 Step Outputs:")
		log.Println(strings.Repeat("=", 60))

		for jobName, jobResult := range execution.JobResults {
			if jobResult.Status == workflow.JobStatusCompleted {
				log.Printf("\n🔧 Job: %s", jobName)
				for stepName, stepResult := range jobResult.Steps {
					if stepResult.Output != "" {
						log.Printf("  📋 Step: %s", stepName)
						log.Printf("     Output:\n%s", stepResult.Output)
					}
				}
			}
		}
	}
}

func printExecutionSummary(execution *workflow.WorkflowExecution) {
	log.Println("\n📊 Execution Summary:")
	log.Println(strings.Repeat("=", 60))

	log.Printf("🆔 Execution ID: %s", execution.ID)
	log.Printf("🕐 Start Time: %s", execution.StartTime.Format("2006-01-02 15:04:05"))
	if execution.EndTime != nil {
		log.Printf("🕐 End Time: %s", execution.EndTime.Format("2006-01-02 15:04:05"))
	}
	log.Printf("⏱️  Duration: %v", execution.Duration)
	log.Printf("📊 Status: %s", execution.Status)

	log.Printf("\n📋 Job Results:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tSTATUS\tDURATION\tSTEPS")

	for jobName, result := range execution.JobResults {
		completedSteps := 0
		for _, stepResult := range result.Steps {
			if stepResult.Status == workflow.JobStatusCompleted {
				completedSteps++
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%v\t%d/%d\n",
			jobName,
			result.Status,
			result.Duration.Round(time.Second),
			completedSteps,
			len(result.Steps))
	}

	w.Flush()
}
