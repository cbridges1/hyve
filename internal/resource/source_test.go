package resource

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeSource struct {
	data   []byte
	err    error
	called bool
}

func (s *fakeSource) GetResource(name string) ([]byte, error) {
	s.called = true
	return s.data, s.err
}

func TestChainSource_PrimaryFound_FallbackNotCalled(t *testing.T) {
	primary := &fakeSource{data: []byte("from-primary")}
	fallback := &fakeSource{data: []byte("from-fallback")}

	data, err := ChainSource{Primary: primary, Fallback: fallback}.GetResource("x")
	require.NoError(t, err)
	assert.Equal(t, []byte("from-primary"), data)
	assert.False(t, fallback.called, "fallback must not be consulted when primary succeeds")
}

func TestChainSource_PrimaryNotFound_FallsBackToFallback(t *testing.T) {
	notFoundErr := apierrors.NewNotFound(schema.GroupResource{Group: "hyve.io", Resource: "resources"}, "x")
	primary := &fakeSource{err: notFoundErr}
	fallback := &fakeSource{data: []byte("from-fallback")}

	data, err := ChainSource{Primary: primary, Fallback: fallback}.GetResource("x")
	require.NoError(t, err)
	assert.Equal(t, []byte("from-fallback"), data)
	assert.True(t, fallback.called)
}

func TestChainSource_PrimaryRealError_NeverFallsBack(t *testing.T) {
	primary := &fakeSource{err: errors.New("connection refused")}
	fallback := &fakeSource{data: []byte("from-fallback")}

	_, err := ChainSource{Primary: primary, Fallback: fallback}.GetResource("x")
	require.Error(t, err)
	assert.False(t, fallback.called, "a real (non-NotFound) primary error must not trigger a fallback attempt")
}

func TestFileSource_GetResource_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	content := "apiVersion: hyve.io/v1alpha1\nkind: Resource\nmetadata:\n  name: nginx\nspec:\n  manifest: |\n    kind: Deployment\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nginx.yaml"), []byte(content), 0o644))

	src := FileSource{Dir: dir}
	data, err := src.GetResource("nginx")
	require.NoError(t, err)
	assert.Equal(t, "kind: Deployment\n", string(data))
}

func TestFileSource_GetResource_NotFound(t *testing.T) {
	src := FileSource{Dir: t.TempDir()}
	_, err := src.GetResource("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDecodeResource_RejectsWrongKind(t *testing.T) {
	_, err := decodeResource("x.yaml", []byte("apiVersion: hyve.io/v1alpha1\nkind: Workflow\nspec:\n  manifest: foo\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apiVersion/kind must be")
}

func TestDecodeResource_RejectsWrongAPIVersion(t *testing.T) {
	_, err := decodeResource("x.yaml", []byte("apiVersion: v1\nkind: Resource\nspec:\n  manifest: foo\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apiVersion/kind must be")
}
