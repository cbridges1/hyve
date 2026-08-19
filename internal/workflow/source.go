package workflow

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Source resolves a local-name WorkflowRef (see types.WorkflowRef.Name) into
// a runnable Workflow definition — the one thing that differs between local
// file mode and cluster mode when running a lifecycle-hook or ad-hoc
// workflow; everything else (the executor, step runners, variable
// injection) is unchanged regardless of which Source is in play.
type Source interface {
	GetWorkflow(name string) (*Workflow, error)
}

// FileSource reads workflows/<name>.yaml under Dir — today's original
// behavior local mode has always used, and controller mode's fallback once
// ChainSource can't find a Workflow CR (see its own doc comment).
type FileSource struct {
	Dir string
}

func (s FileSource) GetWorkflow(name string) (*Workflow, error) {
	filePath := filepath.Join(s.Dir, name+WorkflowFileExt)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workflow '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}
	return decodeWorkflow(filePath, data)
}

// CRDSource reads a Workflow custom resource by name — the controller-mode
// default: workflows are supposed to be cluster-native resources, not
// files baked into the controller's container image.
type CRDSource struct {
	Client    client.Client
	Namespace string
}

func (s CRDSource) GetWorkflow(name string) (*Workflow, error) {
	var cr hyvev1alpha1.Workflow
	if err := s.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: s.Namespace, Name: name}, &cr); err != nil {
		return nil, err
	}
	return toWorkflow(&cr), nil
}

// ChainSource tries Primary first and falls back to Fallback only when
// Primary reports the workflow doesn't exist there (a real lookup error —
// anything other than "not found" — is returned as-is, not silently
// papered over by a fallback attempt). This is what lets an existing
// controller deployment relying entirely on baked-in workflows/ files keep
// working unmodified after this change, while a Workflow CR — once one
// exists with the same name — takes priority without needing the image
// rebuilt.
type ChainSource struct {
	Primary  Source
	Fallback Source
}

func (s ChainSource) GetWorkflow(name string) (*Workflow, error) {
	wf, err := s.Primary.GetWorkflow(name)
	if err == nil {
		return wf, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	log.Printf("workflow %q resolved from baked-in image, not a Workflow CR — consider migrating it", name)
	return s.Fallback.GetWorkflow(name)
}
