package resources

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func helmEntry() Entry {
	return Entry{
		Name: "external-dns", Namespace: "external-dns", Secrets: []string{"cloudflare-dns"},
		HelmOp: map[string]any{"helm": map[string]any{
			"repo": "https://kubernetes-sigs.github.io/external-dns/", "chart": "external-dns", "version": "1.19.0",
			"values": map[string]any{"provider": map[string]any{"name": "cloudflare"}, "txtOwnerId": "${ .ClusterName }"},
		}},
	}
}

func TestValidate(t *testing.T) {
	for name, e := range map[string]Entry{
		"two kinds":       {Name: "a", HelmOp: map[string]any{"helm": map[string]any{"chart": "x"}}, GitRepo: map[string]any{"repo": "r"}},
		"none":            {Name: "a"},
		"values w/o ref":  {Name: "a", HelmOp: map[string]any{"helm": map[string]any{"chart": "x"}}, Values: map[string]any{"a": 1}},
		"git secrets":     {Name: "a", GitRepo: map[string]any{"repo": "r"}, Secrets: []string{"s"}},
		"no chart":        {Name: "a", HelmOp: map[string]any{"helm": map[string]any{"repo": "https://x"}}},
		"empty bundle":    {Name: "a", Bundle: map[string]any{}},
		"targets present": {Name: "a", Bundle: map[string]any{"resources": []any{map[string]any{}}, "targets": []any{}}},
	} {
		if err := e.Validate(true); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	oci := Entry{Name: "a", HelmOp: map[string]any{"helm": map[string]any{"repo": "oci://ghcr.io/x/chart", "version": "1"}}}
	if err := oci.Validate(false); err != nil {
		t.Errorf("an OCI repo needs no chart: %v", err)
	}
	if err := (&Entry{Name: "a", Ref: "x"}).Validate(false); err == nil {
		t.Error("a shared Resource can't be a ref")
	}
}

func TestResolveRef(t *testing.T) {
	shared := helmEntry()
	lookups := 0
	lookup := func(_ context.Context, ns, name string) (*Entry, error) {
		lookups++
		if ns != "capi-clusters" || name != "external-dns" {
			return nil, errors.New("not found")
		}
		s := shared
		return &s, nil
	}
	e := Entry{Name: "dns", Ref: "external-dns", Secrets: []string{"extra"}, Values: map[string]any{"provider": map[string]any{"zone": "z"}, "policy": "sync"}}
	r, err := Resolve(context.Background(), e, "capi-clusters", lookup)
	if err != nil {
		t.Fatal(err)
	}
	vals := r.HelmOp["helm"].(map[string]any)["values"].(map[string]any)
	if vals["policy"] != "sync" || vals["provider"].(map[string]any)["name"] != "cloudflare" || vals["provider"].(map[string]any)["zone"] != "z" {
		t.Errorf("values = %v", vals)
	}
	if r.Name != "dns" || strings.Join(r.Secrets, ",") != "cloudflare-dns,extra" || r.Namespace != "external-dns" {
		t.Errorf("resolved = %+v", r)
	}
	// The shared definition itself is untouched.
	if _, ok := shared.HelmOp["helm"].(map[string]any)["values"].(map[string]any)["policy"]; ok {
		t.Error("Resolve changed the shared Resource")
	}
	if _, err := Resolve(context.Background(), Entry{Name: "x", Ref: "missing"}, "capi-clusters", lookup); err == nil {
		t.Error("a missing ref must fail")
	}
}

func TestObjectNameIsShortened(t *testing.T) {
	n := ObjectName(Owner{Kind: OwnerTemplate, Name: strings.Repeat("t", 40)}, "resource-with-a-long-name")
	if len(n) > 50 || !strings.HasPrefix(n, "tpl-") {
		t.Errorf("name %q (%d)", n, len(n))
	}
}
