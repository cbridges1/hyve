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
	"github.com/cbridges1/hyve/internal/workflow"
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
		listWorkflows()
	},
}

var workflowShowCmd = &cobra.Command{
	Use:   "show [workflow-name]",
	Short: "Show workflow details",
	Long:  "Display detailed information about a specific workflow.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		showWorkflow(args[0])
	},
}

var workflowRunCmd = &cobra.Command{
	Use:   "run [workflow-name]",
	Short: "Run a workflow",
	Long: `Execute a workflow on a cluster.
If no cluster is specified, the workflow will run without cluster context (local commands only).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, _ := cmd.Flags().GetString("cluster")
		showLogs, _ := cmd.Flags().GetBool("logs")
		showOutput, _ := cmd.Flags().GetBool("output")

		runWorkflow(args[0], cluster, showLogs, showOutput)
	},
}

var workflowDeleteCmd = &cobra.Command{
	Use:   "delete [workflow-name]",
	Short: "Delete a workflow",
	Long:  "Remove a workflow definition from the repository.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		deleteWorkflow(args[0], force)
	},
}

var workflowValidateCmd = &cobra.Command{
	Use:   "validate [workflow-name]",
	Short: "Validate a workflow",
	Long:  "Validate the syntax and structure of a workflow definition.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
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

	if len(workflows) == 0 {
		log.Println("No workflows found in repository")
		log.Printf("💡 Create a workflow with: hyve workflow create --template my-workflow")
		return
	}

	log.Printf("📋 Workflows in repository (%d):\n", len(workflows))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tJOBS\tCREATED")

	for _, wf := range workflows {
		created := wf.Metadata.Created.Format("2006-01-02")
		if wf.Metadata.Created.IsZero() {
			created = "unknown"
		}

		description := wf.Metadata.Description
		if len(description) > 50 {
			description = description[:47] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
			wf.Metadata.Name,
			description,
			len(wf.Spec.Jobs),
			created)
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
		log.Fatalf("Failed to get workflow: %v", err)
	}

	log.Printf("📋 Workflow: %s", wf.Metadata.Name)
	if wf.Metadata.Description != "" {
		log.Printf("📝 Description: %s", wf.Metadata.Description)
	}
	log.Printf("📅 Created: %s", wf.Metadata.Created.Format("2006-01-02 15:04:05"))
	log.Printf("📅 Updated: %s", wf.Metadata.Updated.Format("2006-01-02 15:04:05"))

	if len(wf.Metadata.Labels) > 0 {
		log.Printf("🏷️  Labels:")
		for key, value := range wf.Metadata.Labels {
			log.Printf("   %s: %s", key, value)
		}
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

func runWorkflow(name, cluster string, showLogs, showOutput bool) {
	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}

	executor, err := workflow.NewExecutor(manager, cluster)
	if err != nil {
		log.Fatalf("Failed to create workflow executor: %v", err)
	}
	defer executor.Close()

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

	errors := []string{}
	warnings := []string{}

	if wf.APIVersion == "" {
		errors = append(errors, "Missing apiVersion")
	} else if wf.APIVersion != "v1" {
		warnings = append(warnings, fmt.Sprintf("Unexpected apiVersion '%s', expected 'v1'", wf.APIVersion))
	}

	if wf.Kind == "" {
		errors = append(errors, "Missing kind")
	} else if wf.Kind != "Workflow" {
		errors = append(errors, fmt.Sprintf("Invalid kind '%s', expected 'Workflow'", wf.Kind))
	}

	if wf.Metadata.Name == "" {
		errors = append(errors, "Missing metadata.name")
	}

	if len(wf.Spec.Jobs) == 0 {
		errors = append(errors, "No jobs defined in workflow")
	}

	jobNames := make(map[string]bool)
	for i, job := range wf.Spec.Jobs {
		if job.Name == "" {
			errors = append(errors, fmt.Sprintf("Job %d is missing a name", i+1))
			continue
		}

		if jobNames[job.Name] {
			errors = append(errors, fmt.Sprintf("Duplicate job name: %s", job.Name))
		}
		jobNames[job.Name] = true

		if len(job.Steps) == 0 {
			errors = append(errors, fmt.Sprintf("Job '%s' has no steps", job.Name))
		}

		for j, step := range job.Steps {
			if step.Name == "" {
				warnings = append(warnings, fmt.Sprintf("Job '%s', step %d is missing a name", job.Name, j+1))
			}

			hasExecution := step.Command != "" || step.Script != "" || step.Action != ""
			if !hasExecution {
				errors = append(errors, fmt.Sprintf("Job '%s', step '%s' has no command, script, or action", job.Name, step.Name))
			}

			methods := 0
			if step.Command != "" {
				methods++
			}
			if step.Script != "" {
				methods++
			}
			if step.Action != "" {
				methods++
			}
			if methods > 1 {
				errors = append(errors, fmt.Sprintf("Job '%s', step '%s' has multiple execution methods (command/script/action)", job.Name, step.Name))
			}

			if step.Action != "" {
				switch step.Action {
				case "kubectl-apply":
					if step.With == nil || step.With["file"] == "" {
						errors = append(errors, fmt.Sprintf("Job '%s', step '%s': kubectl-apply action requires 'file' parameter", job.Name, step.Name))
					}
				case "kubectl-delete":
					if step.With == nil || step.With["file"] == "" {
						errors = append(errors, fmt.Sprintf("Job '%s', step '%s': kubectl-delete action requires 'file' parameter", job.Name, step.Name))
					}
				default:
					warnings = append(warnings, fmt.Sprintf("Job '%s', step '%s': unknown action '%s'", job.Name, step.Name, step.Action))
				}
			}
		}
	}

	for _, job := range wf.Spec.Jobs {
		for _, dep := range job.DependsOn {
			if !jobNames[dep] {
				errors = append(errors, fmt.Sprintf("Job '%s' depends on non-existent job '%s'", job.Name, dep))
			}
		}
	}

	if hasCircularDependencies(wf.Spec.Jobs) {
		errors = append(errors, "Circular dependency detected in job dependencies")
	}

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

func hasCircularDependencies(jobs []workflow.WorkflowJob) bool {
	graph := make(map[string][]string)
	for _, job := range jobs {
		graph[job.Name] = job.DependsOn
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(string) bool
	hasCycle = func(jobName string) bool {
		visited[jobName] = true
		recStack[jobName] = true

		for _, dep := range graph[jobName] {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if recStack[dep] {
				return true
			}
		}

		recStack[jobName] = false
		return false
	}

	for _, job := range jobs {
		if !visited[job.Name] {
			if hasCycle(job.Name) {
				return true
			}
		}
	}

	return false
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
