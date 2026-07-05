package cmd

import "fmt"

// version/commit/date are set via -ldflags at build time by GoReleaser
// (see .goreleaser.yaml). They default to these values for `go build`/`go
// run`/`go install` without ldflags, so local dev builds still report
// something sensible from `hyve --version`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate(fmt.Sprintf("hyve %s (commit %s, built %s)\n", version, commit, date))
}
