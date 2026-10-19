package install

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/kube"
)

// Local mode's lean install (open question 5): Cluster API's and CAAPF's
// release manifests applied directly, without the CAPI Operator, and
// without cert-manager. Each provider's webhook certificate is made here
// instead, from one CA, and its CA put straight into the webhook
// configurations and CRD conversion webhooks cert-manager would have
// injected it into. That saves the operator, cert-manager's three pods and
// their CRDs. Upgrades mean re-running the install, which is what local
// mode does anyway.

//go:embed manifests/lean/*.yaml
var leanManifests embed.FS

// LeanProviders are the manifests for each provider name, in install order.
var LeanProviders = map[string][]string{
	"core":    {"core-components.yaml"},
	"kubeadm": {"bootstrap-components.yaml", "control-plane-components.yaml"},
	"docker":  {"bootstrap-components.yaml", "control-plane-components.yaml", "infrastructure-components-development.yaml"},
	"caapf":   {"addon-components.yaml"},
}

// LeanOptions tune the lean install.
type LeanOptions struct {
	// GOGC for every controller, to trade CPU for memory.
	GOGC string
	Log  func(format string, args ...any)
}

var varRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-=])([^}]*))?\}`)

// substitute does clusterctl's variable substitution: ${VAR},
// ${VAR:=default}, ${VAR:-default}, ${VAR=default}.
func substitute(manifest []byte, vars map[string]string) ([]byte, error) {
	var missing []string
	out := varRE.ReplaceAllFunc(manifest, func(m []byte) []byte {
		g := varRE.FindSubmatch(m)
		name, op, def := string(g[1]), string(g[2]), string(g[3])
		if v, ok := vars[name]; ok && (v != "" || op == "=" || op == "-") {
			return []byte(v)
		}
		if op != "" {
			// Defaults are written as YAML (`""`, `:8443`); keep them as is.
			return []byte(def)
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("variables without a value: %v", missing)
	}
	return out, nil
}

// LeanInstall applies the named providers' manifests (see LeanProviders),
// once each, with certificates made here.
func LeanInstall(ctx context.Context, kc *kube.Client, providers []string, o LeanOptions) error {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	vars := map[string]string{}
	for k, v := range FeatureVariables {
		vars[k] = v
	}
	ca, caKey, err := leanCA(ctx, kc)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range providers {
		files, ok := LeanProviders[p]
		if !ok {
			return fmt.Errorf("unknown provider %q for the lean install", p)
		}
		for _, f := range files {
			if seen[f] {
				continue
			}
			seen[f] = true
			raw, err := leanManifests.ReadFile("manifests/lean/" + f)
			if err != nil {
				return err
			}
			raw, err = substitute(raw, vars)
			if err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			objs, err := kube.Decode(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			out, err := withoutCertManager(objs, ca, caKey)
			if err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			if o.GOGC != "" {
				setGOGC(out, o.GOGC)
			}
			logf("Installing %s", f)
			var manifest []byte
			for _, obj := range out {
				b, err := obj.MarshalJSON()
				if err != nil {
					return err
				}
				manifest = append(manifest, []byte("---\n")...)
				manifest = append(manifest, b...)
				manifest = append(manifest, '\n')
			}
			if err := applyRetrying(ctx, kc, manifest, logf); err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
		}
	}
	return nil
}

// withoutCertManager drops Issuers and Certificates, makes each
// Certificate's Secret itself, and sets the CA where cert-manager would
// have injected it.
func withoutCertManager(objs []*unstructured.Unstructured, ca *x509.Certificate, caKey *ecdsa.PrivateKey) ([]*unstructured.Unstructured, error) {
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	caB64 := base64.StdEncoding.EncodeToString(caPEM)
	var out []*unstructured.Unstructured
	for _, o := range objs {
		if o.GroupVersionKind().Group == "cert-manager.io" {
			if o.GetKind() != "Certificate" {
				continue
			}
			secret, _, _ := unstructured.NestedString(o.Object, "spec", "secretName")
			names, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "dnsNames")
			crt, key, err := serving(ca, caKey, names)
			if err != nil {
				return nil, err
			}
			out = append(out, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls",
				"metadata": map[string]any{"name": secret, "namespace": o.GetNamespace(), "labels": map[string]any{"app.kubernetes.io/managed-by": "hyve"}},
				"data": map[string]any{
					"tls.crt": base64.StdEncoding.EncodeToString(crt),
					"tls.key": base64.StdEncoding.EncodeToString(key),
					"ca.crt":  caB64,
				},
			}})
			continue
		}
		ann := o.GetAnnotations()
		if _, ok := ann["cert-manager.io/inject-ca-from"]; ok {
			delete(ann, "cert-manager.io/inject-ca-from")
			o.SetAnnotations(ann)
			switch o.GetKind() {
			case "ValidatingWebhookConfiguration", "MutatingWebhookConfiguration":
				hooks, _, _ := unstructured.NestedSlice(o.Object, "webhooks")
				for _, h := range hooks {
					if m, ok := h.(map[string]any); ok {
						_ = unstructured.SetNestedField(m, caB64, "clientConfig", "caBundle")
					}
				}
				_ = unstructured.SetNestedSlice(o.Object, hooks, "webhooks")
			case "CustomResourceDefinition":
				if _, found, _ := unstructured.NestedMap(o.Object, "spec", "conversion", "webhook", "clientConfig"); found {
					_ = unstructured.SetNestedField(o.Object, caB64, "spec", "conversion", "webhook", "clientConfig", "caBundle")
				}
			}
		}
		out = append(out, o)
	}
	return out, nil
}

func setGOGC(objs []*unstructured.Unstructured, gogc string) {
	for _, o := range objs {
		if o.GetKind() != "Deployment" {
			continue
		}
		cs, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "containers")
		for _, c := range cs {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			env, _ := m["env"].([]any)
			m["env"] = append(env, map[string]any{"name": "GOGC", "value": gogc})
		}
		_ = unstructured.SetNestedSlice(o.Object, cs, "spec", "template", "spec", "containers")
	}
}

// leanCA keeps the CA the lean install signs webhook certificates with in
// a Secret, so re-running it keeps the same CA.
func leanCA(ctx context.Context, kc *kube.Client) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	ri := kc.Dynamic.Resource(secretGVR).Namespace("kube-system")
	if s, err := ri.Get(ctx, "hyve-webhook-ca", getOpts); err == nil {
		crtB64, _, _ := unstructured.NestedString(s.Object, "data", "tls.crt")
		keyB64, _, _ := unstructured.NestedString(s.Object, "data", "tls.key")
		crtPEM, _ := base64.StdEncoding.DecodeString(crtB64)
		keyPEM, _ := base64.StdEncoding.DecodeString(keyB64)
		cb, _ := pem.Decode(crtPEM)
		kb, _ := pem.Decode(keyPEM)
		if cb != nil && kb != nil {
			ca, err1 := x509.ParseCertificate(cb.Bytes)
			key, err2 := x509.ParseECPrivateKey(kb.Bytes)
			if err1 == nil && err2 == nil {
				return ca, key, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "hyve-webhook-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	err = kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls",
		"metadata": map[string]any{"name": "hyve-webhook-ca", "namespace": "kube-system"},
		"data": map[string]any{
			"tls.crt": base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			"tls.key": base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		},
	}})
	return ca, key, err
}

func serving(ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string) (crtPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	cn := "webhook"
	if len(names) > 0 {
		cn = names[0]
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}
