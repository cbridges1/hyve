package types

// ClusterState holds the reconciler-owned observed-state fields for one
// cluster, persisted separately from that cluster's desired-state YAML (see
// state.Manager.SaveClusterDefinition/LoadClusterDefinition) so machine
// bookkeeping (content hashes, timestamps, tracked-object lists) stops
// polluting the human-authored file and its git diffs. Never hand-edit —
// same contract as the ClusterSpec fields it mirrors.
type ClusterState struct {
	DriverOutputs    map[string]string           `yaml:"driverOutputs,omitempty" json:"driverOutputs,omitempty"`
	AppliedResources map[string]*AppliedResource `yaml:"appliedResources,omitempty" json:"appliedResources,omitempty"`
}

// IsEmpty reports whether there is nothing worth persisting to a sidecar
// file (e.g. a brand-new cluster before its first create/apply cycle).
func (s ClusterState) IsEmpty() bool {
	return len(s.DriverOutputs) == 0 && len(s.AppliedResources) == 0
}
