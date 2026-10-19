package operator

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ensureCerts writes a fresh CA and a serving certificate for the webhook
// Service into dir (tls.crt, tls.key) and returns the CA PEM. A new pair on
// every start keeps it simple; patchCABundle tells the API server.
func ensureCerts(dir, service, namespace string) ([]byte, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "hyve-controller-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	host := fmt.Sprintf("%s.%s.svc", service, namespace)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{service, service + "." + namespace, host, host + ".cluster.local"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	write := func(name, typ string, b []byte) error {
		return os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600)
	}
	if err := write("tls.crt", "CERTIFICATE", der); err != nil {
		return nil, err
	}
	if err := write("tls.key", "EC PRIVATE KEY", keyDER); err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}

// patchCABundles sets every webhook's caBundle in the named mutating and
// validating webhook configurations (either may be "").
func patchCABundles(ctx context.Context, cfg *rest.Config, mutating, validating string, ca []byte) error {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	if validating != "" {
		vwc, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, validating, metav1.GetOptions{})
		if err != nil {
			return err
		}
		changed := false
		for i := range vwc.Webhooks {
			if !bytes.Equal(vwc.Webhooks[i].ClientConfig.CABundle, ca) {
				vwc.Webhooks[i].ClientConfig.CABundle = ca
				changed = true
			}
		}
		if changed {
			if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(ctx, vwc, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}
	if mutating != "" {
		mwc, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, mutating, metav1.GetOptions{})
		if err != nil {
			return err
		}
		changed := false
		for i := range mwc.Webhooks {
			if !bytes.Equal(mwc.Webhooks[i].ClientConfig.CABundle, ca) {
				mwc.Webhooks[i].ClientConfig.CABundle = ca
				changed = true
			}
		}
		if changed {
			if _, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(ctx, mwc, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}
	return nil
}
