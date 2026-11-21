package operator

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// FleetAgentTLS has fleet-agents trust the host's certificate when they
// reach the management cluster through the host (Fleet's apiServerURL is
// under HostURL) and that certificate is publicly trusted (the host was
// given no CA). Fleet otherwise hands agents a CA of its own to pin, in
// agentTLSMode strict: the local cluster's, or one left over from Rancher,
// which the host's certificate doesn't chain to, so the agents never
// register. It sets agentTLSMode to system-store and clears apiServerCA in
// fleet-controller's ConfigMap; fleet-controller redeploys the agents.
// CAAPF and Fleet's chart write the rest of that ConfigMap, and a Helm
// upgrade can put the old values back, so it's checked again every Every.
type FleetAgentTLS struct {
	Reader  client.Reader
	Client  client.Client
	HostURL string
	Every   time.Duration
}

// Start implements manager.Runnable.
func (f *FleetAgentTLS) Start(ctx context.Context) error {
	t := time.NewTicker(f.Every)
	defer t.Stop()
	for {
		if err := f.Ensure(ctx); err != nil {
			log.FromContext(ctx).Info("couldn't set Fleet's agent TLS", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Ensure sets agentTLSMode and apiServerCA where they'd make agents
// refuse the host's certificate.
func (f *FleetAgentTLS) Ensure(ctx context.Context) error {
	host := strings.TrimRight(f.HostURL, "/")
	for _, ns := range fleetNamespaces {
		var cm corev1.ConfigMap
		if err := f.Reader.Get(ctx, types.NamespacedName{Namespace: ns, Name: "fleet-controller"}, &cm); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		cfg := map[string]any{}
		if err := json.Unmarshal([]byte(cm.Data["config"]), &cfg); err != nil {
			return err
		}
		url, _ := cfg["apiServerURL"].(string)
		if host == "" || !strings.HasPrefix(url, host+"/") {
			continue
		}
		if cfg["agentTLSMode"] == "system-store" && cfg["apiServerCA"] == "" {
			continue
		}
		cfg["agentTLSMode"] = "system-store"
		cfg["apiServerCA"] = ""
		out, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		cm.Data["config"] = string(out)
		if err := f.Client.Update(ctx, &cm, client.FieldOwner(FieldManager)); err != nil {
			return err
		}
		log.FromContext(ctx).Info("had fleet-agents trust the host's certificate (agentTLSMode system-store)", "configmap", ns+"/fleet-controller")
	}
	return nil
}
