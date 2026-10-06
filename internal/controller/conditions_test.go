package controller

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// TestClusterConditions: Ready is True only for an ACTIVE cluster whose
// reconcile succeeded — a successful pass over a still-creating cluster is
// not ready.
func TestClusterConditions(t *testing.T) {
	ready := func(c []metav1.Condition) metav1.Condition {
		for _, x := range c {
			if x.Type == hyvev1alpha1.ConditionTypeReady {
				return x
			}
		}
		t.Fatal("no Ready condition")
		return metav1.Condition{}
	}
	for _, tc := range []struct {
		phase      string
		err        error
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"ACTIVE", nil, metav1.ConditionTrue, "Active"},
		{"CREATING", nil, metav1.ConditionFalse, "Creating"},
		{"NOT_FOUND", nil, metav1.ConditionFalse, "NotFound"},
		{"DELETING", nil, metav1.ConditionFalse, "Deleting"},
		{"", nil, metav1.ConditionFalse, "Unknown"},
		{"ACTIVE", errors.New("boom"), metav1.ConditionFalse, "ReconcileFailed"},
	} {
		c := ready(clusterConditions(tc.phase, tc.err))
		assert.Equal(t, tc.wantStatus, c.Status, tc.phase)
		assert.Equal(t, tc.wantReason, c.Reason, tc.phase)
	}
}
