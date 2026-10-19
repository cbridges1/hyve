package install

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/cbridges1/hyve/internal/kube"
)

func pod(name, image, reason, msg string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": "hyve-system", "labels": map[string]any{"app": "hyve"}},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": image}}},
		"status": map[string]any{"containerStatuses": []any{map[string]any{
			"name": "c", "image": "docker.io/library/" + image,
			"state": map[string]any{"waiting": map[string]any{"reason": reason, "message": msg}},
		}}},
	}}
}

func deployment(image string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "hyve"}},
			"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "c", "image": image}}}},
		},
	}}
}

func TestPullFailure(t *testing.T) {
	notFound := "failed to resolve reference: not found"
	for _, tc := range []struct {
		name   string
		pods   []runtime.Object
		image  string
		wantOK bool
	}{
		{"missing image", []runtime.Object{pod("a", "hyve:x", "ImagePullBackOff", notFound)}, "hyve:x", false},
		{"older rollout's pod", []runtime.Object{pod("a", "hyve:old", "ImagePullBackOff", notFound)}, "hyve:new", true},
		{"transient error", []runtime.Object{pod("a", "hyve:x", "ErrImagePull", "i/o timeout")}, "hyve:x", true},
		{"still creating", []runtime.Object{pod("a", "hyve:x", "ContainerCreating", "")}, "hyve:x", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kc := &kube.Client{Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), tc.pods...)}
			err := pullFailure(context.Background(), kc, "hyve-system", deployment(tc.image))
			if (err == nil) != tc.wantOK {
				t.Fatalf("pullFailure = %v, want ok=%v", err, tc.wantOK)
			}
			if err != nil && !strings.Contains(err.Error(), tc.image) {
				t.Errorf("error doesn't name the image: %v", err)
			}
		})
	}
}

func TestDevBuildNeedsAnImage(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	Version = "dev"
	if _, err := Install(context.Background(), nil, Options{}); err != ErrNoImage {
		t.Fatalf("Install = %v, want ErrNoImage", err)
	}
}
