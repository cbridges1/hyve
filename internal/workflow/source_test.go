package workflow

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeSource struct {
	wf     *Workflow
	err    error
	called bool
}

func (s *fakeSource) GetWorkflow(name string) (*Workflow, error) {
	s.called = true
	return s.wf, s.err
}

func TestChainSource_PrimaryFound_FallbackNotCalled(t *testing.T) {
	primary := &fakeSource{wf: &Workflow{Metadata: WorkflowMetadata{Name: "from-primary"}}}
	fallback := &fakeSource{wf: &Workflow{Metadata: WorkflowMetadata{Name: "from-fallback"}}}

	wf, err := ChainSource{Primary: primary, Fallback: fallback}.GetWorkflow("x")
	require.NoError(t, err)
	assert.Equal(t, "from-primary", wf.Metadata.Name)
	assert.False(t, fallback.called, "fallback must not be consulted when primary succeeds")
}

func TestChainSource_PrimaryNotFound_FallsBackToFallback(t *testing.T) {
	notFoundErr := apierrors.NewNotFound(schema.GroupResource{Group: "hyve.io", Resource: "workflows"}, "x")
	primary := &fakeSource{err: notFoundErr}
	fallback := &fakeSource{wf: &Workflow{Metadata: WorkflowMetadata{Name: "from-fallback"}}}

	wf, err := ChainSource{Primary: primary, Fallback: fallback}.GetWorkflow("x")
	require.NoError(t, err)
	assert.Equal(t, "from-fallback", wf.Metadata.Name)
	assert.True(t, fallback.called)
}

func TestChainSource_PrimaryRealError_NeverFallsBack(t *testing.T) {
	primary := &fakeSource{err: errors.New("connection refused")}
	fallback := &fakeSource{wf: &Workflow{Metadata: WorkflowMetadata{Name: "from-fallback"}}}

	_, err := ChainSource{Primary: primary, Fallback: fallback}.GetWorkflow("x")
	require.Error(t, err)
	assert.False(t, fallback.called, "a real (non-NotFound) primary error must not trigger a fallback attempt")
}

func TestManager_GetWorkflow_DelegatesToSource(t *testing.T) {
	src := &fakeSource{wf: &Workflow{Metadata: WorkflowMetadata{Name: "delegated"}}}
	mgr, err := NewManagerWithSource(t.TempDir(), src)
	require.NoError(t, err)

	wf, err := mgr.GetWorkflow("whatever")
	require.NoError(t, err)
	assert.Equal(t, "delegated", wf.Metadata.Name)
	assert.True(t, src.called)
}
