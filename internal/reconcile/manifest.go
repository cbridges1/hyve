package reconcile

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/types"
)

// parseManifestObjects splits data (possibly multiple "---"-separated YAML
// documents) into minimal object identities. Uses a streaming yaml.Decoder
// rather than string-splitting on "---" so it handles YAML's own comment/
// quoting rules correctly. Skips empty documents (e.g. a leading or trailing
// "---" with nothing between). If a document's own metadata.namespace is
// empty, defaultNamespace (the ResourceRef.Namespace, "" if unset) is used —
// matching what `kubectl apply -n <namespace>` actually does for objects
// lacking their own namespace.
//
// This repo has no k8s client-go dependency and none is added here — only
// the handful of fields needed (apiVersion/kind/metadata.name/namespace) are
// decoded, not full k8s API types.
func parseManifestObjects(data []byte, defaultNamespace string) ([]types.AppliedObject, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []types.AppliedObject
	for {
		var doc struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse manifest document: %w", err)
		}
		if doc.Kind == "" && doc.APIVersion == "" && doc.Metadata.Name == "" {
			continue // empty document (e.g. a leading/trailing "---")
		}
		ns := doc.Metadata.Namespace
		if ns == "" {
			ns = defaultNamespace
		}
		out = append(out, types.AppliedObject{
			APIVersion: doc.APIVersion,
			Kind:       doc.Kind,
			Namespace:  ns,
			Name:       doc.Metadata.Name,
		})
	}
	return out, nil
}
