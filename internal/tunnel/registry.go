// Package tunnel is the server side of Rancher's agent tunnel, so hyve can
// be the Rancher host for its child clusters when Rancher isn't installed
// (HYVE-PROPOSAL.md, "hyve as the Rancher host").
//
// Each child cluster runs Rancher's own cattle-cluster-agent, with its
// multi-cluster-management feature off so it only keeps the tunnel open. It
// dials /v3/connect/register (first time) or /v3/connect with a
// registration token, and reports its cluster's API address, CA and the
// token of its "cattle" service account in X-API-Tunnel-Params, exactly as
// it does for Rancher (rancher/rancher cmd/agent, pkg/tunnelserver). hyve
// then reaches that API through the agent's tunnel, at
// /k8s/clusters/<id>, as the signed-in user.
//
// What hyve knows about each agent is one Secret in its namespace: the
// registration token's hash (as a label, to find the cluster by token),
// and what the agent last reported.
package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelAgent marks a cluster's agent Secret.
	LabelAgent = "hyve.io/agent"
	// LabelTokenHash finds a cluster by its registration token.
	LabelTokenHash = "hyve.io/token-hash"
	// AnnotationClusterID is the cluster's ID, <namespace>.<name>.
	AnnotationClusterID = "hyve.io/cluster-id"

	keyToken    = "registrationToken"
	keyAddress  = "address"
	keyCACert   = "caCert"
	keySAToken  = "serviceAccountToken"
	tokenLength = 32
)

// ErrUnknownToken means no cluster has that registration token.
var ErrUnknownToken = errors.New("unknown registration token")

// ClusterID is a CAPI cluster's ID in proxy paths: <namespace>.<name>.
// Both are DNS labels, so the dot is unambiguous.
func ClusterID(namespace, name string) string { return namespace + "." + name }

// SplitClusterID reverses ClusterID.
func SplitClusterID(id string) (namespace, name string, ok bool) {
	return strings.Cut(id, ".")
}

// SecretName is the agent Secret's name for a cluster: a hash of its ID,
// since namespace and name together can be longer than a name allows.
func SecretName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "hyve-agent-" + hex.EncodeToString(sum[:])[:16]
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:40]
}

// Agent is what hyve knows about one cluster's agent.
type Agent struct {
	ClusterID         string
	RegistrationToken string
	// Reported by the agent; empty until it has connected once.
	Address             string
	CACert              []byte
	ServiceAccountToken string
}

// Registry keeps agents' Secrets in Namespace.
type Registry struct {
	Client    client.Client
	Namespace string
}

func (r *Registry) get(ctx context.Context, id string) (*corev1.Secret, error) {
	s := &corev1.Secret{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: SecretName(id)}, s)
	return s, err
}

func toAgent(s *corev1.Secret) *Agent {
	return &Agent{
		ClusterID:           s.Annotations[AnnotationClusterID],
		RegistrationToken:   string(s.Data[keyToken]),
		Address:             string(s.Data[keyAddress]),
		CACert:              s.Data[keyCACert],
		ServiceAccountToken: string(s.Data[keySAToken]),
	}
}

// Ensure returns a cluster's agent, creating its registration token the
// first time.
func (r *Registry) Ensure(ctx context.Context, id string) (*Agent, error) {
	s, err := r.get(ctx, id)
	if err == nil {
		return toAgent(s), nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	b := make([]byte, tokenLength)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)
	s = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: SecretName(id), Namespace: r.Namespace,
			Labels:      map[string]string{LabelAgent: "true", LabelTokenHash: tokenHash(token)},
			Annotations: map[string]string{AnnotationClusterID: id},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{keyToken: []byte(token)},
	}
	if err := r.Client.Create(ctx, s); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return r.Get(ctx, id)
		}
		return nil, err
	}
	return toAgent(s), nil
}

// Get returns a cluster's agent.
func (r *Registry) Get(ctx context.Context, id string) (*Agent, error) {
	s, err := r.get(ctx, id)
	if err != nil {
		return nil, err
	}
	return toAgent(s), nil
}

// Delete forgets a cluster's agent; its token stops working.
func (r *Registry) Delete(ctx context.Context, id string) error {
	err := r.Client.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretName(id), Namespace: r.Namespace}})
	return client.IgnoreNotFound(err)
}

// ByToken finds the cluster a registration token belongs to.
func (r *Registry) ByToken(ctx context.Context, token string) (*Agent, error) {
	if token == "" {
		return nil, ErrUnknownToken
	}
	list := &corev1.SecretList{}
	sel := labels.SelectorFromSet(labels.Set{LabelAgent: "true", LabelTokenHash: tokenHash(token)})
	if err := r.Client.List(ctx, list, client.InNamespace(r.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if a := toAgent(&list.Items[i]); constantEqual(a.RegistrationToken, token) {
			return a, nil
		}
	}
	return nil, ErrUnknownToken
}

// Report records what an agent says about its cluster.
func (r *Registry) Report(ctx context.Context, id, address string, caCert []byte, saToken string) error {
	s, err := r.get(ctx, id)
	if err != nil {
		return err
	}
	if string(s.Data[keyAddress]) == address && string(s.Data[keyCACert]) == string(caCert) && string(s.Data[keySAToken]) == saToken {
		return nil
	}
	s.Data[keyAddress] = []byte(address)
	s.Data[keyCACert] = caCert
	s.Data[keySAToken] = []byte(saToken)
	return r.Client.Update(ctx, s)
}

func constantEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// clusterParams is the part of X-API-Tunnel-Params a cluster agent sends.
type clusterParams struct {
	Cluster *struct {
		Address string `json:"address"`
		Token   string `json:"token"`
		CACert  string `json:"caCert"`
	} `json:"cluster"`
}

func decodeCA(b64 string) ([]byte, error) {
	ca, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("caCert: %w", err)
	}
	return ca, nil
}
