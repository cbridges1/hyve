package shared

import (
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// JoinWorkflowRefs renders a []types.WorkflowRef list for display —
// WorkflowRef.String() handles both local names and remote sources.
func JoinWorkflowRefs(refs []types.WorkflowRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

// LoadManifest loads the module.yaml for the given driver source/version.
// Returns nil if the manifest is not available locally.
func LoadManifest(source, version string) *module.ModuleManifest {
	repoRoot := GetRepoPath()
	lf, _ := module.LoadLockFile(repoRoot)
	m, _ := module.LoadManifestForSource(source, version, repoRoot, lf)
	return m
}

// CollectParamValues runs a per-param form when a manifest is available.
// Choice params use a dropdown; free-text params use an input field.
// Falls back to a raw KEY=VALUE input when manifest is nil or has no params.
// existing is used to pre-populate values (e.g. when editing overrides).
func CollectParamValues(manifest *module.ModuleManifest, existing map[string]string, fallbackTitle string) (map[string]string, error) {
	if manifest == nil || len(manifest.Spec.Params) == 0 {
		var paramsRaw string
		err := NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fallbackTitle).
					Description("KEY=VALUE pairs, comma-separated. e.g. node_size=g4s.kube.medium,node_count=3").
					Value(&paramsRaw),
			),
		).Run()
		if err != nil {
			return nil, err
		}
		return ParseParamOverrides(paramsRaw), nil
	}

	params := manifest.Spec.Params
	values := make([]string, len(params))
	for i, p := range params {
		values[i] = p.Default
		if existing != nil {
			if v, ok := existing[p.Name]; ok {
				values[i] = v
			}
		}
	}

	fields := make([]huh.Field, 0, len(params))
	for i, p := range params {
		title := p.Name
		if p.Description != "" {
			title = p.Name + " — " + p.Description
		}
		if len(p.Choices) > 0 {
			opts := make([]huh.Option[string], len(p.Choices))
			for j, c := range p.Choices {
				opts[j] = huh.NewOption(c, c)
			}
			fields = append(fields, huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(&values[i]))
		} else {
			fields = append(fields, huh.NewInput().
				Title(title).
				Value(&values[i]))
		}
	}

	if err := NewForm(huh.NewGroup(fields...)).Run(); err != nil {
		return nil, err
	}

	result := make(map[string]string, len(params))
	for i, p := range params {
		if values[i] != "" {
			result[p.Name] = values[i]
		}
	}
	return result, nil
}

// ParseParamOverrides splits a "key=value,key2=value2" string into a map.
func ParseParamOverrides(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}
