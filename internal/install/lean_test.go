package install

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"math/big"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/kube"
)

var kubeDecode = kube.Decode

func TestSubstitute(t *testing.T) {
	in := []byte(`a: ${A}
b: ${B:=8443}
c: ${C:=""}
d: --feature-gates=ClusterTopology=${CLUSTER_TOPOLOGY:=false}`)
	out, err := substitute(in, map[string]string{"A": "x", "CLUSTER_TOPOLOGY": "true"})
	if err != nil {
		t.Fatal(err)
	}
	want := `a: x
b: 8443
c: ""
d: --feature-gates=ClusterTopology=true`
	if string(out) != want {
		t.Errorf("got\n%s", out)
	}
	if _, err := substitute([]byte("${MISSING}"), nil); err == nil {
		t.Error("a variable with no value and no default must fail")
	}
}

func TestLeanManifestsLoseCertManager(t *testing.T) {
	for _, files := range LeanProviders {
		for _, f := range files {
			raw, err := leanManifests.ReadFile("manifests/lean/" + f)
			if err != nil {
				t.Fatal(err)
			}
			vars := map[string]string{}
			for k, v := range FeatureVariables {
				vars[k] = v
			}
			if _, err := substitute(raw, vars); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	}
}

func TestWithoutCertManager(t *testing.T) {
	raw, _ := leanManifests.ReadFile("manifests/lean/core-components.yaml")
	vars := map[string]string{}
	for k, v := range FeatureVariables {
		vars[k] = v
	}
	raw, _ = substitute(raw, vars)
	objs, err := kubeDecode(raw)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	ca, _ := x509.ParseCertificate(der)
	out, err := withoutCertManager(objs, ca, key)
	if err != nil {
		t.Fatal(err)
	}
	secrets, injected := 0, 0
	for _, o := range out {
		if o.GroupVersionKind().Group == "cert-manager.io" {
			t.Fatalf("cert-manager object left: %s %s", o.GetKind(), o.GetName())
		}
		if _, ok := o.GetAnnotations()["cert-manager.io/inject-ca-from"]; ok {
			t.Fatalf("injection annotation left on %s", o.GetName())
		}
		if o.GetKind() == "Secret" && o.GetName() == "capi-webhook-service-cert" {
			secrets++
		}
		if o.GetKind() == "ValidatingWebhookConfiguration" {
			hooks, _, _ := unstructured.NestedSlice(o.Object, "webhooks")
			if b, _, _ := unstructured.NestedString(hooks[0].(map[string]any), "clientConfig", "caBundle"); b != "" {
				injected++
			}
		}
	}
	if secrets != 1 || injected != 1 {
		t.Errorf("secrets=%d injected webhook configs=%d", secrets, injected)
	}
}
