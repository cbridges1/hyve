package api

import (
	"context"
	"fmt"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FindBindingBySubject returns the HyveAccessBinding matching
// subjectType/subjectValue (e.g. "local"/"cedric"). Used both at login time
// (to find the paired credentials Secret) and by the authz middleware (to
// resolve a role per-request — see authz.go), so a role change on a
// binding takes effect on the very next request, not just the next login.
// More than one match is a configuration error (ambiguous), not something
// to silently resolve by picking the first.
func FindBindingBySubject(ctx context.Context, c client.Client, subjectType, subjectValue string) (*hyvev1alpha1.HyveAccessBinding, error) {
	var list hyvev1alpha1.HyveAccessBindingList
	if err := c.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list HyveAccessBindings: %w", err)
	}
	var matches []hyvev1alpha1.HyveAccessBinding
	for _, b := range list.Items {
		if b.Spec.Subject.Type == subjectType && b.Spec.Subject.Value == subjectValue {
			matches = append(matches, b)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no HyveAccessBinding found for %s subject %q", subjectType, subjectValue)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("%d HyveAccessBindings match %s subject %q — ambiguous, remove the duplicate", len(matches), subjectType, subjectValue)
	}
}
