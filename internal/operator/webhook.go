package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/templates"
)

// getTemplate returns the ClusterTemplate a Cluster is labelled with, or
// nil when it has none.
func getTemplate(ctx context.Context, c client.Client, cluster *unstructured.Unstructured) (*templates.Template, error) {
	name, ns := templates.Ref(cluster)
	if name == "" {
		return nil, nil
	}
	u := object(templateGVK)
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		return nil, err
	}
	return templates.FromUnstructured(u)
}

// FillCluster completes a Cluster labelled with a ClusterTemplate when it's
// created (templates.Fill).
type FillCluster struct {
	Client client.Client
	Now    func() time.Time
}

// Handle implements admission.Handler.
func (h *FillCluster) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create {
		return admission.Allowed("")
	}
	c := &unstructured.Unstructured{}
	if err := json.Unmarshal(req.Object.Raw, &c.Object); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if c.GetNamespace() == "" {
		c.SetNamespace(req.Namespace)
	}
	t, err := getTemplate(ctx, h.Client, c)
	if apierrors.IsNotFound(err) {
		name, ns := templates.Ref(c)
		return admission.Denied(fmt.Sprintf("ClusterTemplate %s/%s not found", ns, name))
	}
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if t == nil {
		return admission.Allowed("")
	}
	class := object(clusters.ClusterClassGVR.GroupVersion().WithKind("ClusterClass"))
	if err := h.Client.Get(ctx, types.NamespacedName{Namespace: t.ClassNamespace(), Name: t.Spec.ClusterClassRef.Name}, class); err != nil {
		return admission.Denied(fmt.Sprintf("ClusterTemplate %s: ClusterClass %s/%s: %v", t.Name, t.ClassNamespace(), t.Spec.ClusterClassRef.Name, err))
	}
	if err := templates.Fill(c, t, class, h.Now()); err != nil {
		return admission.Denied(err.Error())
	}
	filled, err := json.Marshal(c.Object)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, filled)
}

// LockedVariables rejects a Cluster labelled with a ClusterTemplate whose
// locked variables differ from the template's, so the Rancher UI and
// kubectl can't get around what the template sets.
type LockedVariables struct {
	Client client.Client
}

// Handle implements admission.Handler.
func (h *LockedVariables) Handle(ctx context.Context, req admission.Request) admission.Response {
	c := &unstructured.Unstructured{}
	if err := json.Unmarshal(req.Object.Raw, &c.Object); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if c.GetDeletionTimestamp() != nil {
		return admission.Allowed("")
	}
	if c.GetNamespace() == "" {
		c.SetNamespace(req.Namespace)
	}
	t, err := getTemplate(ctx, h.Client, c)
	if err != nil || t == nil {
		// A deleted template locks nothing.
		return admission.Allowed("")
	}
	bad := templates.CheckLocked(t, c)
	if req.Operation == admissionv1.Update && len(bad) > 0 {
		// Only a change to a locked variable is refused. A cluster made
		// before the lock was added keeps its value, and every other update
		// to it (Cluster API's own included) still goes through.
		old := &unstructured.Unstructured{}
		if err := json.Unmarshal(req.OldObject.Raw, &old.Object); err == nil {
			bad = templates.ChangedVariables(old, c, bad)
		}
	}
	if len(bad) > 0 {
		return admission.Denied(fmt.Sprintf("ClusterTemplate %s locks %s: they must keep the template's values", t.Name, strings.Join(bad, ", ")))
	}
	return admission.Allowed("")
}

// RecordCreator records who created a Cluster in an environment, in
// hyve.io/creator, so hyve-controller can make them its owner. The value
// comes from the API server's view of the request, never the client: on
// create it's set to the requester (a hyve user) or removed, and on update
// it keeps its old value.
type RecordCreator struct{}

// Handle implements admission.Handler.
func (h *RecordCreator) Handle(_ context.Context, req admission.Request) admission.Response {
	c := &unstructured.Unstructured{}
	if err := json.Unmarshal(req.Object.Raw, &c.Object); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	want := ""
	switch req.Operation {
	case admissionv1.Create:
		// A hyve user (standalone), or a Rancher user ID as Rancher's proxy
		// impersonates it (with Rancher).
		if u := req.UserInfo.Username; strings.HasPrefix(u, "hyve:") || IsRancherUser(u) {
			want = u
		}
	case admissionv1.Update:
		old := &unstructured.Unstructured{}
		if err := json.Unmarshal(req.OldObject.Raw, &old.Object); err == nil {
			want = old.GetAnnotations()[AnnotationCreator]
		}
	default:
		return admission.Allowed("")
	}
	ann := c.GetAnnotations()
	if ann[AnnotationCreator] == want {
		return admission.Allowed("")
	}
	if ann == nil {
		ann = map[string]string{}
	}
	if want == "" {
		delete(ann, AnnotationCreator)
	} else {
		ann[AnnotationCreator] = want
	}
	c.SetAnnotations(ann)
	out, err := json.Marshal(c.Object)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, out)
}
