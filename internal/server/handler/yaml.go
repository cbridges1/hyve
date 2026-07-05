package handler

import (
	"io"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

const yamlMediaType = "application/x-yaml"

// wantsYAML reports whether the request prefers a raw YAML response over
// the default JSON representation.
func wantsYAML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), yamlMediaType)
}

// isYAMLBody reports whether the request body is YAML rather than JSON.
func isYAMLBody(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), yamlMediaType)
}

// writeResource writes v according to the request's Accept header:
// application/x-yaml round-trips exact YAML bytes when rawYAML is supplied
// (byte-for-byte, preserving comments/key order as stored in the repo);
// everything else gets the default JSON representation.
func writeResource(w http.ResponseWriter, r *http.Request, status int, v interface{}, rawYAML []byte) {
	if wantsYAML(r) {
		w.Header().Set("Content-Type", yamlMediaType)
		if rawYAML != nil {
			w.WriteHeader(status)
			w.Write(rawYAML)
			return
		}
		data, err := yaml.Marshal(v)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to marshal YAML: "+err.Error())
			return
		}
		w.WriteHeader(status)
		w.Write(data)
		return
	}
	writeJSON(w, status, v)
}

// decodeResource reads the request body into v, parsing it as YAML or JSON
// based on Content-Type. Returns the raw bytes read (for callers that want to
// persist the exact submitted YAML rather than re-marshal from v).
func decodeResource(w http.ResponseWriter, r *http.Request, v interface{}) ([]byte, bool) {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body required")
		return nil, false
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}

	if isYAMLBody(r) {
		if err := yaml.Unmarshal(data, v); err != nil {
			writeError(w, http.StatusBadRequest, "invalid YAML body: "+err.Error())
			return nil, false
		}
	} else {
		if err := yaml.Unmarshal(data, v); err != nil { // yaml.Unmarshal parses JSON too (JSON is a YAML subset)
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return nil, false
		}
	}
	return data, true
}
