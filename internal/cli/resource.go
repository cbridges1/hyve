package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// movedCmd is a command group that's been replaced: it and each named
// subcommand say what to use instead.
func movedCmd(use, short, hint string, subs ...string) *cobra.Command {
	run := func(*cobra.Command, []string) error { return errors.New(hint) }
	cmd := &cobra.Command{Use: use, Short: short, Hidden: true, DisableFlagParsing: true, RunE: run}
	for _, sub := range subs {
		cmd.AddCommand(&cobra.Command{Use: sub, Short: short, DisableFlagParsing: true, RunE: run})
	}
	return cmd
}

// ResourceCmd was `hyve resource`: shared Resources, which Deployments
// replaced.
func ResourceCmd() *cobra.Command {
	return movedCmd("resource", "Replaced by 'hyve deploy'",
		"shared resources are gone: a Deployment is defined once and targets every template, class, environment "+
			"or cluster it's for ('hyve deploy create NAME ... --to template/A --to template/B'); 'hyve deploy list' lists them",
		"create", "apply", "list", "show", "status", "delete")
}

// clusterResourceCmd was `hyve cluster resource`: one cluster's extras.
func clusterResourceCmd() *cobra.Command {
	return movedCmd("resource", "Replaced by 'hyve deploy'",
		"a cluster's extras are Deployments now: 'hyve deploy create NAME ... --to cluster/ENV/CLUSTER' adds one, "+
			"'hyve cluster deployments CLUSTER' lists what's on a cluster",
		"add", "remove", "list")
}

// MovedTo is cmd at its old place, after it moved: called by its old name
// (use), hidden, and saying on it and each subcommand what to run instead
// (at, the new path, such as "template render"). It still works.
func MovedTo(cmd *cobra.Command, use, at string) *cobra.Command {
	if _, rest, ok := strings.Cut(cmd.Use, " "); ok {
		cmd.Use = use + " " + rest
	} else {
		cmd.Use = use
	}
	cmd.Aliases = nil
	cmd.Hidden = true
	var mark func(c *cobra.Command, path string)
	mark = func(c *cobra.Command, path string) {
		c.Deprecated = fmt.Sprintf("it's 'hyve %s' now", path)
		for _, sub := range c.Commands() {
			mark(sub, path+" "+sub.Name())
		}
	}
	mark(cmd, at)
	return cmd
}

// RepoCmd was `hyve repo`: plain Fleet GitRepos, which a Deployment with a
// git source is.
func RepoCmd() *cobra.Command {
	return movedCmd("repo", "Replaced by 'hyve deploy'",
		"a Fleet GitRepo is a Deployment with a git source: 'hyve deploy create NAME --git URL [--git-path P] [--branch B] "+
			"[--username U --token-env VAR] --to host' (or --to template/..., cluster/...); 'hyve deploy list', show and delete manage them",
		"add", "list", "show", "remove")
}
