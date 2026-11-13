package local

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePKIReissuesForANewCA(t *testing.T) {
	dir := t.TempDir()
	_, cert1, _, err := ensurePKI(dir, "172.18.0.2", "hyve-local")
	if err != nil {
		t.Fatal(err)
	}
	_, again, _, err := ensurePKI(dir, "172.18.0.2", "hyve-local")
	if err != nil || !bytes.Equal(cert1, again) {
		t.Fatalf("the host certificate should be kept: %v", err)
	}
	// The CA's files are lost: it's made again, and so is the host's.
	for _, f := range []string{"ca.crt", "ca.key"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	ca, cert2, _, err := ensurePKI(dir, "172.18.0.2", "hyve-local")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(cert1, cert2) || !signedBy(cert2, ca) {
		t.Fatal("the host certificate isn't signed by the new CA")
	}
}
