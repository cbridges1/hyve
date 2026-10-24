package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"sigs.k8s.io/yaml"

	hyvechart "github.com/cbridges1/hyve/deploy/helm"
)

// RenderChart renders hyve's embedded chart with values merged over its
// values.yaml, as `helm template` would for this chart: its CRDs first,
// then its templates. It supports what hyve's chart uses (sprig, include,
// .Values, .Chart, .Release); it isn't a general Helm.
func RenderChart(namespace string, values map[string]any) ([]byte, error) {
	root := "hyve"
	read := func(p string) ([]byte, error) { return fs.ReadFile(hyvechart.Chart, path.Join(root, p)) }
	rawValues, err := read("values.yaml")
	if err != nil {
		return nil, err
	}
	vals := map[string]any{}
	if err := yaml.Unmarshal(rawValues, &vals); err != nil {
		return nil, fmt.Errorf("values.yaml: %w", err)
	}
	mergeValues(vals, values)
	rawChart, err := read("Chart.yaml")
	if err != nil {
		return nil, err
	}
	chart := map[string]any{}
	if err := yaml.Unmarshal(rawChart, &chart); err != nil {
		return nil, err
	}
	// Templates see .Chart.AppVersion, as Helm capitalizes it.
	chartData := map[string]any{"Name": chart["name"], "Version": chart["version"], "AppVersion": chart["appVersion"]}
	data := map[string]any{
		"Values":  vals,
		"Chart":   chartData,
		"Release": map[string]any{"Name": "hyve", "Namespace": namespace, "Service": "hyve"},
	}

	var out bytes.Buffer
	crds, _ := fs.Glob(hyvechart.Chart, root+"/crds/*.yaml")
	sort.Strings(crds)
	for _, p := range crds {
		b, err := fs.ReadFile(hyvechart.Chart, p)
		if err != nil {
			return nil, err
		}
		out.WriteString("---\n")
		out.Write(b)
		out.WriteString("\n")
	}

	tpl := template.New("hyve").Funcs(sprig.TxtFuncMap())
	tpl.Funcs(template.FuncMap{
		"include": func(name string, d any) (string, error) {
			var b bytes.Buffer
			err := tpl.ExecuteTemplate(&b, name, d)
			return b.String(), err
		},
		// Helm's required: the value, or an error with msg when it's empty.
		"required": func(msg string, v any) (any, error) {
			if v == nil || v == "" {
				return nil, errors.New(msg)
			}
			return v, nil
		},
		"toYaml": func(v any) string {
			b, err := yaml.Marshal(v)
			if err != nil {
				return ""
			}
			return strings.TrimSuffix(string(b), "\n")
		},
	})
	files, _ := fs.Glob(hyvechart.Chart, root+"/templates/*")
	sort.Strings(files)
	for _, p := range files {
		b, err := fs.ReadFile(hyvechart.Chart, p)
		if err != nil {
			return nil, err
		}
		if _, err := tpl.New(path.Base(p)).Parse(string(b)); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	for _, p := range files {
		name := path.Base(p)
		if strings.HasPrefix(name, "_") {
			continue
		}
		var b bytes.Buffer
		if err := tpl.ExecuteTemplate(&b, name, data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out.WriteString("---\n")
		out.Write(b.Bytes())
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}

func mergeValues(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeValues(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}
