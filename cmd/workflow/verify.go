package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/workflowref"
)

var workflowVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify that every locked workflow's cached content still matches its sha256",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)

		lf, err := mod.LoadLockFile(stateMgr.LocalPath())
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}

		failed := 0
		for key, w := range lf.Workflows {
			if w.SHA256 == "" || !workflowref.IsCached(w.SHA256) {
				log.Printf("❌ %s (name=%s): not cached — run `hyve workflow install`", key, w.Name)
				failed++
				continue
			}
			data, err := workflowref.ReadCached(w.SHA256)
			if err != nil {
				log.Printf("❌ %s: failed to read cache: %v", key, err)
				failed++
				continue
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != w.SHA256 {
				log.Printf("❌ %s: cached content does not match sha256 (corrupted cache)", key)
				failed++
				continue
			}
			log.Printf("✅ %s (name=%s)", key, w.Name)
		}

		if failed > 0 {
			log.Fatalf("%d workflow(s) failed verification", failed)
		}
		log.Println("✅ All locked workflows verified")
	},
}

func init() { workflowCmd.AddCommand(workflowVerifyCmd) }
