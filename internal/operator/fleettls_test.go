package operator

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fleetConfig(t *testing.T, cfg map[string]any) *corev1.ConfigMap {
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "cattle-fleet-system", Name: "fleet-controller"}, Data: map[string]string{"config": string(b)}}
}

func TestFleetAgentTLSTrustsTheHost(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().WithObjects(fleetConfig(t, map[string]any{
		"apiServerURL": "https://hyve.example.com/k8s/clusters/local", "apiServerCA": "-----BEGIN CERTIFICATE-----", "agentTLSMode": "strict", "agentCheckinInterval": "15m",
	})).Build()
	if err := (&FleetAgentTLS{Reader: cl, Client: cl, HostURL: "https://hyve.example.com/"}).Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "cattle-fleet-system", Name: "fleet-controller"}, &cm); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	_ = json.Unmarshal([]byte(cm.Data["config"]), &got)
	if got["agentTLSMode"] != "system-store" || got["apiServerCA"] != "" || got["agentCheckinInterval"] != "15m" {
		t.Errorf("config = %v", got)
	}
}

func TestFleetAgentTLSLeavesOtherServers(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().WithObjects(fleetConfig(t, map[string]any{
		"apiServerURL": "https://10.0.0.1:6443", "apiServerCA": "ca", "agentTLSMode": "strict",
	})).Build()
	if err := (&FleetAgentTLS{Reader: cl, Client: cl, HostURL: "https://hyve.example.com"}).Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	_ = cl.Get(ctx, types.NamespacedName{Namespace: "cattle-fleet-system", Name: "fleet-controller"}, &cm)
	got := map[string]any{}
	_ = json.Unmarshal([]byte(cm.Data["config"]), &got)
	if got["agentTLSMode"] != "strict" || got["apiServerCA"] != "ca" {
		t.Errorf("changed a config for another server: %v", got)
	}
}
