package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/types"
)

func TestParseManifestObjects_SingleDoc(t *testing.T) {
	data := []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx\n  namespace: web\n")
	objs, err := parseManifestObjects(data, "")
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, types.AppliedObject{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "web", Name: "nginx"}, objs[0])
}

func TestParseManifestObjects_MultiDoc(t *testing.T) {
	data := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: web
---
apiVersion: v1
kind: Service
metadata:
  name: nginx-svc
  namespace: web
`)
	objs, err := parseManifestObjects(data, "")
	require.NoError(t, err)
	require.Len(t, objs, 2)
	assert.Equal(t, "Deployment", objs[0].Kind)
	assert.Equal(t, "nginx", objs[0].Name)
	assert.Equal(t, "Service", objs[1].Kind)
	assert.Equal(t, "nginx-svc", objs[1].Name)
}

func TestParseManifestObjects_LeadingEmptyDoc(t *testing.T) {
	data := []byte("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n")
	objs, err := parseManifestObjects(data, "")
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, "ConfigMap", objs[0].Kind)
}

func TestParseManifestObjects_TrailingEmptyDoc(t *testing.T) {
	data := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n---\n")
	objs, err := parseManifestObjects(data, "")
	require.NoError(t, err)
	require.Len(t, objs, 1)
}

func TestParseManifestObjects_NoNamespaceUsesDefault(t *testing.T) {
	data := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n")
	objs, err := parseManifestObjects(data, "ingress-nginx")
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, "ingress-nginx", objs[0].Namespace)
}

func TestParseManifestObjects_OwnNamespaceWins(t *testing.T) {
	data := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n  namespace: custom\n")
	objs, err := parseManifestObjects(data, "ingress-nginx")
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, "custom", objs[0].Namespace)
}

func TestParseManifestObjects_InvalidYAML(t *testing.T) {
	_, err := parseManifestObjects([]byte("not: valid: yaml: :::"), "")
	assert.Error(t, err)
}

func TestParseManifestObjects_EmptyInput(t *testing.T) {
	objs, err := parseManifestObjects([]byte(""), "")
	require.NoError(t, err)
	assert.Empty(t, objs)
}
