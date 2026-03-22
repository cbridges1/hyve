package civo

import (
	"testing"
	"time"

	"github.com/civo/civogo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestProvider returns a Provider with no real Civo client, suitable for
// testing pure logic methods that do not call the Civo API.
func newTestProvider(region string) *Provider {
	return &Provider{
		client: nil,
		region: region,
	}
}

// ── Name / Region ─────────────────────────────────────────────────────────────

func TestName(t *testing.T) {
	p := newTestProvider("LON1")
	assert.Equal(t, "civo", p.Name())
}

func TestRegion(t *testing.T) {
	p := newTestProvider("NYC1")
	assert.Equal(t, "NYC1", p.Region())
}

// ── convertCluster ────────────────────────────────────────────────────────────

func TestConvertCluster_AllFields(t *testing.T) {
	p := newTestProvider("LON1")
	ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	raw := &civogo.KubernetesCluster{
		ID:         "cluster-abc",
		Name:       "prod",
		Status:     "ACTIVE",
		FirewallID: "fw-123",
		MasterIP:   "10.0.0.1",
		KubeConfig: "kubeconfig-data",
		CreatedAt:  ts,
	}

	got := p.convertCluster(raw)

	require.NotNil(t, got)
	assert.Equal(t, "cluster-abc", got.ID)
	assert.Equal(t, "prod", got.Name)
	assert.Equal(t, "ACTIVE", got.Status)
	assert.Equal(t, "fw-123", got.FirewallID)
	assert.Equal(t, "10.0.0.1", got.MasterIP)
	assert.Equal(t, "kubeconfig-data", got.KubeConfig)
	assert.Equal(t, ts, got.CreatedAt)
}

func TestConvertCluster_EmptyFields(t *testing.T) {
	p := newTestProvider("LON1")
	got := p.convertCluster(&civogo.KubernetesCluster{})

	require.NotNil(t, got)
	assert.Empty(t, got.ID)
	assert.Empty(t, got.Name)
	assert.Empty(t, got.Status)
	assert.Empty(t, got.FirewallID)
	assert.Empty(t, got.MasterIP)
	assert.Empty(t, got.KubeConfig)
	assert.True(t, got.CreatedAt.IsZero())
}

// ── convertFirewall ───────────────────────────────────────────────────────────

func TestConvertFirewall_WithRules(t *testing.T) {
	p := newTestProvider("LON1")

	raw := &civogo.Firewall{
		ID:   "fw-1",
		Name: "my-fw",
		Rules: []civogo.FirewallRule{
			{
				Protocol:  "tcp",
				StartPort: "80",
				EndPort:   "80",
				Cidr:      []string{"0.0.0.0/0"},
				Direction: "ingress",
			},
			{
				Protocol:  "tcp",
				StartPort: "443",
				EndPort:   "443",
				Cidr:      []string{"0.0.0.0/0", "::/0"},
				Direction: "ingress",
			},
		},
	}

	got := p.convertFirewall(raw)

	require.NotNil(t, got)
	assert.Equal(t, "fw-1", got.ID)
	assert.Equal(t, "my-fw", got.Name)
	require.Len(t, got.Rules, 2)

	assert.Equal(t, "tcp", got.Rules[0].Protocol)
	assert.Equal(t, "80", got.Rules[0].StartPort)
	assert.Equal(t, "80", got.Rules[0].EndPort)
	assert.Equal(t, []string{"0.0.0.0/0"}, got.Rules[0].Cidr)
	assert.Equal(t, "ingress", got.Rules[0].Direction)

	assert.Equal(t, []string{"0.0.0.0/0", "::/0"}, got.Rules[1].Cidr)
}

func TestConvertFirewall_EmptyRules(t *testing.T) {
	p := newTestProvider("LON1")
	raw := &civogo.Firewall{ID: "fw-2", Name: "empty"}

	got := p.convertFirewall(raw)

	require.NotNil(t, got)
	assert.Equal(t, "fw-2", got.ID)
	assert.Empty(t, got.Rules)
}

func TestConvertFirewall_EmptyFields(t *testing.T) {
	p := newTestProvider("LON1")
	got := p.convertFirewall(&civogo.Firewall{})

	require.NotNil(t, got)
	assert.Empty(t, got.ID)
	assert.Empty(t, got.Name)
	assert.Nil(t, got.Rules)
}
