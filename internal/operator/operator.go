// Package operator is hyve-controller: it runs on the management cluster
// and does what can't be left to the CLI (HYVE-PROPOSAL.md, "Controller"):
//
//   - templates: a mutating webhook fills in each Cluster labelled with a
//     ClusterTemplate as it's created, and a validating webhook keeps its
//     locked variables locked;
//   - template resources: each ClusterTemplate's resources become Fleet
//     objects aimed at its clusters, and each ClusterResource one aimed at
//     its cluster;
//   - expiry: Clusters whose hyve.io/expires-at has passed are deleted.
package operator

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
	"github.com/cbridges1/hyve/internal/tunnel"
)

// FieldManager is the server-side-apply field manager hyve-controller uses.
const FieldManager = "hyve-controller"

var (
	clusterGVK         = schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta2", Kind: "Cluster"}
	templateGVK        = templates.GVR.GroupVersion().WithKind("ClusterTemplate")
	resourceGVK        = resources.ResourceGVR.GroupVersion().WithKind("Resource")
	clusterResourceGVK = resources.ClusterResourceGVR.GroupVersion().WithKind("ClusterResource")
)

func object(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	return u
}

// Options for Run.
type Options struct {
	// Webhook serving: port, and where its certificate is written.
	WebhookPort int
	CertDir     string
	// The Service the webhooks are reached through, and the webhook
	// configurations to patch the generated CA into.
	ServiceName, ServiceNamespace          string
	MutatingWebhookName, ValidatingWebhook string
	// NoWebhook runs only the reconcilers.
	NoWebhook bool
	// Agent registration, when hyve is the Rancher host.
	Agents AgentOptions
	// LazyProviders adds known Cluster API providers when something first
	// needs them (local mode).
	LazyProviders bool
}

// Run runs hyve-controller until ctx ends.
func Run(ctx context.Context, cfg *rest.Config, o Options) error {
	lg := log.FromContext(ctx)
	if !o.NoWebhook {
		ca, err := ensureCerts(o.CertDir, o.ServiceName, o.ServiceNamespace)
		if err != nil {
			return fmt.Errorf("webhook certificate: %w", err)
		}
		if err := patchCABundles(ctx, cfg, o.MutatingWebhookName, o.ValidatingWebhook, ca); err != nil {
			return fmt.Errorf("webhook caBundle: %w", err)
		}
	}
	mgr, err := manager.New(cfg, manager.Options{
		// Secrets (CAPI kubeconfigs, agent tokens, resources' Secrets) are
		// read one at a time, never cached: the controller holds no copy of
		// the cluster's Secrets.
		Client:                 client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}}}},
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: ":8081",
		WebhookServer:          webhook.NewServer(webhook.Options{Port: o.WebhookPort, CertDir: o.CertDir}),
	})
	if err != nil {
		return err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-expiry").For(object(clusterGVK)).
		Complete(&Expiry{Client: mgr.GetClient(), Now: time.Now}); err != nil {
		return err
	}
	if err := setupResources(mgr, dc); err != nil {
		return err
	}
	if o.LazyProviders {
		kc, err := kube.New(cfg)
		if err != nil {
			return err
		}
		if err := setupLazyProviders(mgr, kc); err != nil {
			return err
		}
	}
	if probe, err := client.New(cfg, client.Options{}); err == nil && RancherInstalled(ctx, probe) {
		lg.Info("Rancher is installed: using Rancher's users and roles, filling Turtles' gaps")
		if err := setupRancher(ctx, mgr); err != nil {
			return err
		}
		// Rancher added to a standalone install: its agents are Rancher's now.
		if o.Agents.Namespace != "" {
			if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-release-agents").For(object(clusterGVK)).
				Complete(&ReleaseAgents{Client: mgr.GetClient(), Namespace: o.Agents.Namespace}); err != nil {
				return err
			}
		}
	} else if o.Agents.HostURL != "" {
		// Without Rancher, hyve keeps access too (with Rancher, Rancher's
		// cluster role bindings do).
		if err := setupAccess(mgr); err != nil {
			return err
		}
		reg := &tunnel.Registry{Client: mgr.GetClient(), Namespace: o.Agents.Namespace}
		if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-agents").For(object(clusterGVK)).
			Complete(&Agents{Client: mgr.GetClient(), Registry: reg, Options: o.Agents}); err != nil {
			return err
		}
	} else {
		lg.Info("agent registration is off (no host URL); with Rancher, Rancher installs the agents")
	}
	if !o.NoWebhook {
		mgr.GetWebhookServer().Register("/mutate-cluster", &webhook.Admission{Handler: &FillCluster{Client: mgr.GetClient(), Now: time.Now}})
		mgr.GetWebhookServer().Register("/validate-cluster", &webhook.Admission{Handler: &LockedVariables{Client: mgr.GetClient()}})
		mgr.GetWebhookServer().Register("/creator-cluster", &webhook.Admission{Handler: &RecordCreator{}})
	}
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return err
	}
	lg.Info("starting hyve-controller", "webhook", !o.NoWebhook)
	return mgr.Start(ctx)
}
