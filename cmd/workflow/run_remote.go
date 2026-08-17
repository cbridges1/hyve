package workflow

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/workflow"
	"github.com/cbridges1/hyve/internal/workflowref"
)

// looksLikeRemoteSource distinguishes a remote source string from a local
// workflow name. Local names are validated elsewhere (isValidWorkflowName)
// to be alphanumeric/dash/underscore only — no "/". Any "/" in the argument
// therefore unambiguously signals a remote source string.
func looksLikeRemoteSource(nameOrSource string) bool {
	return strings.Contains(nameOrSource, "/")
}

// runWorkflowByRef is the entry point for `hyve workflow run`. Resolution
// order for a bare name: local workflows/ dir first (existing runWorkflow
// path, unchanged), then hyve.lock remote entries by Name; error if not
// found in either, or if the name matches more than one locked remote entry.
func runWorkflowByRef(nameOrSource, pathFlag, cluster string, showLogs, showOutput bool, setVars map[string]string) {
	if looksLikeRemoteSource(nameOrSource) {
		runRemoteWorkflowSource(nameOrSource, pathFlag, cluster, showLogs, showOutput, setVars)
		return
	}

	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err == nil {
		if _, getErr := manager.GetWorkflow(nameOrSource); getErr == nil {
			runWorkflow(nameOrSource, cluster, showLogs, showOutput, setVars) // existing, unchanged
			return
		}
	}

	repoPath := shared.GetRepoPath()
	lf, err := mod.LoadLockFile(repoPath)
	if err != nil {
		log.Fatalf("Failed to load lock file: %v", err)
	}
	matches := lf.FindLockedWorkflowsByName(nameOrSource)
	switch len(matches) {
	case 0:
		log.Fatalf("workflow %q not found locally or in hyve.lock — run `hyve workflow list` or `hyve workflow install`", nameOrSource)
	case 1:
		full := matches[0].Source
		if matches[0].Version != "" {
			full += "@" + matches[0].Version
		}
		runRemoteWorkflowSource(full, "", cluster, showLogs, showOutput, setVars)
	default:
		var b strings.Builder
		for _, m := range matches {
			fmt.Fprintf(&b, "  %s@%s\n", m.Source, m.Version)
		}
		log.Fatalf("workflow name %q is ambiguous across %d locked sources — run with the full source string instead:\n%s", nameOrSource, len(matches), b.String())
	}
}

// runRemoteWorkflowSource fetches (using hyve.lock as a cache hint, so an
// already-installed source is served from local cache with no network
// call) and executes a remote workflow in one shot WITHOUT writing to
// hyve.lock. A future --save flag would add a lf.SetLockedWorkflow +
// SaveLockFile call right after a successful run.
func runRemoteWorkflowSource(source, pathFlag, cluster string, showLogs, showOutput bool, setVars map[string]string) {
	ps, err := workflowref.ParseSource(source)
	if err != nil {
		log.Fatalf("%v", err)
	}
	ps, warned := workflowref.ApplyPathOverride(ps, pathFlag)
	if warned {
		log.Printf("Warning: both an inline //path and --path were given; --path wins")
	}
	if kind, err := workflowref.ClassifyPath(ps.Path); err != nil {
		log.Fatalf("%v", err)
	} else if kind == workflowref.PathKindDir {
		log.Fatalf("source %q resolves to a directory of workflows — `run` requires exactly one file (append //file.yaml or use --path)", source)
	}

	repoPath := shared.GetRepoPath()
	lf, _ := mod.LoadLockFile(repoPath) // best-effort; workflowref.Resolve is nil-safe

	files, err := workflowref.Resolve(source, pathFlag, lf)
	if err != nil {
		log.Fatalf("Failed to resolve workflow %q: %v", source, err)
	}

	var wf workflow.Workflow
	if err := yaml.Unmarshal(files[0].Data, &wf); err != nil {
		log.Fatalf("Failed to parse workflow: %v", err)
	}

	manager, err := workflow.NewManager(getWorkflowLocalPath())
	if err != nil {
		log.Fatalf("Failed to create workflow manager: %v", err)
	}
	executor, err := workflow.NewExecutor(manager, cluster)
	if err != nil {
		log.Fatalf("Failed to create workflow executor: %v", err)
	}
	defer executor.Close()
	executor.KubeconfigLocator = mod.KubeconfigPathForCluster
	if len(setVars) > 0 {
		executor.InjectVars(setVars)
	}

	displayName := wf.Metadata.Name
	if displayName == "" {
		displayName = source
	}

	log.Printf("🚀 Starting remote workflow '%s' (%s)", displayName, source)
	if cluster != "" {
		log.Printf("🎯 Target cluster: %s", cluster)
	} else {
		log.Printf("💻 Running locally (no cluster context)")
	}
	log.Println()

	execution, err := executor.RunResolvedWorkflow(context.Background(), &wf, source, cluster)
	if err != nil {
		log.Printf("❌ Workflow failed: %v", err)
		if showLogs && execution != nil {
			printExecutionLogs(execution, showOutput)
		}
		os.Exit(1)
	}

	log.Printf("✅ Workflow '%s' completed successfully", displayName)
	log.Printf("⏱️  Duration: %v", execution.Duration)

	if showLogs {
		printExecutionLogs(execution, showOutput)
	}

	printExecutionSummary(execution)
}
