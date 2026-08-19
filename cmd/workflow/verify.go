package workflow

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/workflowref"
)

var workflowVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify that every locked workflow's cached content still matches its sha256",
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve workflow verify` is not supported in cluster mode yet — hyve.lock-based remote-ref resolution is a local-checkout concept only.")
		}
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)

		results, failed, err := workflowref.Verify(stateMgr.LocalPath())
		if err != nil {
			log.Fatalf("%v", err)
		}

		for _, r := range results {
			if r.OK {
				log.Printf("✅ %s (name=%s)", r.Key, r.Name)
			} else {
				log.Printf("❌ %s (name=%s): %s", r.Key, r.Name, r.Reason)
			}
		}

		if failed > 0 {
			log.Fatalf("%d workflow(s) failed verification", failed)
		}
		log.Println("✅ All locked workflows verified")
	},
}

func init() { workflowCmd.AddCommand(workflowVerifyCmd) }
