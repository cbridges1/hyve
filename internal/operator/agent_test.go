package operator

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCAChecksumMatchesTheAgentsCheck(t *testing.T) {
	pem := "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n"
	// run.sh: curl .../v3/settings/cacerts | jq -r .value > file; sha256sum file.
	served := strings.TrimRight(pem, "\n")
	sum := sha256.Sum256([]byte(served + "\n"))
	if got := CAChecksum(pem); got != hex.EncodeToString(sum[:]) {
		t.Errorf("checksum %s", got)
	}
	if CAChecksum("") != "" {
		t.Error("no CA, no checksum")
	}
}

func TestAgentManifest(t *testing.T) {
	objs, err := AgentManifest(AgentOptions{HostURL: "https://hyve.example.com", AdminUser: "admin"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]*unstructured.Unstructured{}
	for _, o := range objs {
		kinds[o.GetKind()+"/"+o.GetName()] = o
	}
	d := kinds["Deployment/cattle-cluster-agent"]
	if d == nil {
		t.Fatalf("no agent Deployment in %v", kinds)
	}
	cs, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")
	c := cs[0].(map[string]any)
	if c["image"] != DefaultAgentImage {
		t.Errorf("image %v", c["image"])
	}
	env := map[string]string{}
	for _, e := range c["env"].([]any) {
		m := e.(map[string]any)
		env[m["name"].(string)] = m["value"].(string)
	}
	if env["CATTLE_FEATURES"] != "multi-cluster-management-agent=false" || env["CATTLE_SERVER"] != "https://hyve.example.com" || env["CATTLE_CA_CHECKSUM"] != "" {
		t.Errorf("env %v", env)
	}
	b := kinds["ClusterRoleBinding/hyve-admin"]
	subj, _, _ := unstructured.NestedSlice(b.Object, "subjects")
	if subj[0].(map[string]any)["name"] != "hyve:admin" {
		t.Errorf("admin binding %v", subj)
	}
	var cred *unstructured.Unstructured
	for k, o := range kinds {
		if strings.HasPrefix(k, "Secret/cattle-credentials-") {
			cred = o
		}
	}
	if cred == nil || cred.Object["stringData"].(map[string]any)["token"] != "tok" {
		t.Errorf("credentials %v", cred)
	}
}
