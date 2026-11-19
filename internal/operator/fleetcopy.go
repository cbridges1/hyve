package operator

import (
	"context"
	"encoding/json"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CopyDownstreamEnv is the fleet-controller setting that turns on
// downstreamResources: without it Fleet copies none of the Secrets and
// ConfigMaps a bundle names (Fleet calls it experimental, up to 0.15 at
// least), so a Deployment's "Secrets to copy" never reach a cluster.
// fleet-controller passes it on to the fleet-agents it deploys.
const CopyDownstreamEnv = "EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM"

// fleetNamespaces are where Fleet's chart puts fleet-controller.
var fleetNamespaces = []string{"cattle-fleet-system", "cattle-fleet-local-system", "fleet-system"}

// FleetCopyDownstream keeps CopyDownstreamEnv on in every container of
// fleet-controller's Deployment. Fleet is installed by its chart (CAAPF,
// or by hand), which has no other switch for it than extraEnv; a Helm
// upgrade keeps an env entry it didn't write, and this puts it back if a
// reinstall drops it.
type FleetCopyDownstream struct {
	Reader client.Reader
	Client client.Client
	Every  time.Duration
}

// Start implements manager.Runnable.
func (f *FleetCopyDownstream) Start(ctx context.Context) error {
	t := time.NewTicker(f.Every)
	defer t.Stop()
	for {
		if err := f.Ensure(ctx); err != nil {
			log.FromContext(ctx).Info("couldn't turn on Fleet's downstreamResources", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Ensure sets CopyDownstreamEnv where it's missing or not "true".
func (f *FleetCopyDownstream) Ensure(ctx context.Context) error {
	for _, ns := range fleetNamespaces {
		var d appsv1.Deployment
		if err := f.Reader.Get(ctx, types.NamespacedName{Namespace: ns, Name: "fleet-controller"}, &d); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		var containers []map[string]any
		for _, c := range d.Spec.Template.Spec.Containers {
			on := false
			for _, e := range c.Env {
				if e.Name == CopyDownstreamEnv && e.Value == "true" && e.ValueFrom == nil {
					on = true
				}
			}
			if !on {
				containers = append(containers, map[string]any{"name": c.Name, "env": []any{map[string]any{"name": CopyDownstreamEnv, "value": "true"}}})
			}
		}
		if len(containers) == 0 {
			continue
		}
		// A strategic merge patch merges containers and env by name, so
		// only this entry changes.
		patch, err := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": containers}}}})
		if err != nil {
			return err
		}
		if err := f.Client.Patch(ctx, &d, client.RawPatch(types.StrategicMergePatchType, patch), client.FieldOwner(FieldManager)); err != nil {
			return err
		}
		log.FromContext(ctx).Info("turned on Fleet's downstreamResources", "deployment", ns+"/fleet-controller")
	}
	return nil
}
