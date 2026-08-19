package v1alpha1

// RenderClusterDefinitionSpec merges tpl's default params with overrides
// (overrides win) and produces the ClusterDefinitionSpec a new cluster
// should be created with. The one place this logic lives — called by both
// local mode (internal/template.Template.GenerateClusterDefinition, via
// internal/crdconv) and cluster mode (POST /api/templates/{name}/render,
// and POST /api/clusters when its request sets a template instead of a
// full spec) — so template rendering behaves identically regardless of
// which mode triggered it.
func RenderClusterDefinitionSpec(tpl TemplateSpec, region string, overrides map[string]string) ClusterDefinitionSpec {
	params := make(map[string]string, len(tpl.Params)+len(overrides))
	for k, v := range tpl.Params {
		params[k] = v
	}
	for k, v := range overrides {
		params[k] = v
	}
	if region == "" {
		region = tpl.Region
	}
	return ClusterDefinitionSpec{
		Region:    region,
		Driver:    tpl.Driver,
		Params:    params,
		Workflows: tpl.Workflows,
		Resources: tpl.Resources,
	}
}
