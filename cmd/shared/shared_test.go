package shared

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseNodeGroup ────────────────────────────────────────────────────────────

func TestParseNodeGroup_MinimalValid(t *testing.T) {
	ng, err := ParseNodeGroup("name=workers,type=t3.medium,count=3")
	require.NoError(t, err)
	assert.Equal(t, "workers", ng.Name)
	assert.Equal(t, "t3.medium", ng.InstanceType)
	assert.Equal(t, 3, ng.Count)
}

func TestParseNodeGroup_AllFields(t *testing.T) {
	ng, err := ParseNodeGroup("name=workers,type=t3.medium,count=3,min=1,max=5,disk=50,spot=true,mode=System")
	require.NoError(t, err)
	assert.Equal(t, "workers", ng.Name)
	assert.Equal(t, "t3.medium", ng.InstanceType)
	assert.Equal(t, 3, ng.Count)
	assert.Equal(t, 1, ng.MinCount)
	assert.Equal(t, 5, ng.MaxCount)
	assert.Equal(t, 50, ng.DiskSize)
	assert.True(t, ng.Spot)
	assert.Equal(t, "System", ng.Mode)
}

func TestParseNodeGroup_SpotFalse(t *testing.T) {
	ng, err := ParseNodeGroup("name=workers,type=t3.small,count=2,spot=false")
	require.NoError(t, err)
	assert.False(t, ng.Spot)
}

func TestParseNodeGroup_CountDefaultsToOne(t *testing.T) {
	// count=0 is invalid and should be coerced to 1
	ng, err := ParseNodeGroup("name=workers,type=t3.small,count=0")
	require.NoError(t, err)
	assert.Equal(t, 1, ng.Count)
}

func TestParseNodeGroup_InstanceTypeAlias(t *testing.T) {
	ng, err := ParseNodeGroup("name=workers,instanceType=m5.large,count=2")
	require.NoError(t, err)
	assert.Equal(t, "m5.large", ng.InstanceType)
}

func TestParseNodeGroup_MissingName(t *testing.T) {
	_, err := ParseNodeGroup("type=t3.medium,count=3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestParseNodeGroup_MissingType(t *testing.T) {
	_, err := ParseNodeGroup("name=workers,count=3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type")
}

func TestParseNodeGroup_InvalidCount(t *testing.T) {
	_, err := ParseNodeGroup("name=workers,type=t3.medium,count=abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count")
}

func TestParseNodeGroup_InvalidMin(t *testing.T) {
	_, err := ParseNodeGroup("name=workers,type=t3.medium,count=3,min=abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "min")
}

func TestParseNodeGroup_InvalidMax(t *testing.T) {
	_, err := ParseNodeGroup("name=workers,type=t3.medium,count=3,max=abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max")
}

func TestParseNodeGroup_InvalidDisk(t *testing.T) {
	_, err := ParseNodeGroup("name=workers,type=t3.medium,count=3,disk=abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk")
}

func TestParseNodeGroup_ExtraWhitespace(t *testing.T) {
	ng, err := ParseNodeGroup("name=workers , type=t3.medium , count=2")
	require.NoError(t, err)
	assert.Equal(t, "workers", ng.Name)
	assert.Equal(t, "t3.medium", ng.InstanceType)
	assert.Equal(t, 2, ng.Count)
}

// ── IsValidProvider ───────────────────────────────────────────────────────────

func TestIsValidProvider_ValidProviders(t *testing.T) {
	for _, p := range []string{"civo", "aws", "gcp", "azure"} {
		assert.True(t, IsValidProvider(p), "expected %s to be valid", p)
	}
}

func TestIsValidProvider_CaseInsensitive(t *testing.T) {
	assert.True(t, IsValidProvider("CIVO"))
	assert.True(t, IsValidProvider("AWS"))
	assert.True(t, IsValidProvider("GCP"))
	assert.True(t, IsValidProvider("Azure"))
}

func TestIsValidProvider_InvalidProvider(t *testing.T) {
	for _, p := range []string{"", "k8s", "digitalocean", "linode"} {
		assert.False(t, IsValidProvider(p), "expected %s to be invalid", p)
	}
}

// ── ValidProvidersString ──────────────────────────────────────────────────────

func TestValidProvidersString_ContainsAll(t *testing.T) {
	s := ValidProvidersString()
	for _, p := range ValidProviders {
		assert.True(t, strings.Contains(s, p), "expected %q in providers string", p)
	}
}

func TestValidProvidersString_NonEmpty(t *testing.T) {
	assert.NotEmpty(t, ValidProvidersString())
}
