package api

import (
	"context"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newBinding(name, subjectValue, role string) *hyvev1alpha1.HyveAccessBinding {
	return &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: subjectValue},
			Role:    role,
		},
	}
}

func TestFindBindingBySubject_Match(t *testing.T) {
	c := newFakeClient(t, newBinding("cedric-admin", "cedric", hyvev1alpha1.RoleAdmin))

	b, err := FindBindingBySubject(context.Background(), c, hyvev1alpha1.SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	assert.Equal(t, "cedric-admin", b.Name)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, b.Spec.Role)
}

func TestFindBindingBySubject_NoMatch(t *testing.T) {
	c := newFakeClient(t, newBinding("cedric-admin", "cedric", hyvev1alpha1.RoleAdmin))

	_, err := FindBindingBySubject(context.Background(), c, hyvev1alpha1.SubjectTypeLocal, "someone-else")
	assert.Error(t, err)
}

func TestFindBindingBySubject_NoMatch_EmptyStore(t *testing.T) {
	c := newFakeClient(t)

	_, err := FindBindingBySubject(context.Background(), c, hyvev1alpha1.SubjectTypeLocal, "cedric")
	assert.Error(t, err)
}

func TestFindBindingBySubject_TypeMustAlsoMatch(t *testing.T) {
	// Same Value, different Type — must not match a local lookup.
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric-oidc-only"},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeOIDC, Value: "cedric"},
			Role:    hyvev1alpha1.RoleAdmin,
		},
	}
	c := newFakeClient(t, binding)

	_, err := FindBindingBySubject(context.Background(), c, hyvev1alpha1.SubjectTypeLocal, "cedric")
	assert.Error(t, err, "an oidc-typed binding must not satisfy a local subject lookup")
}

func TestFindBindingBySubject_AmbiguousIsAnError(t *testing.T) {
	c := newFakeClient(t,
		newBinding("cedric-a", "cedric", hyvev1alpha1.RoleAdmin),
		newBinding("cedric-b", "cedric", hyvev1alpha1.RoleReadOnly),
	)

	_, err := FindBindingBySubject(context.Background(), c, hyvev1alpha1.SubjectTypeLocal, "cedric")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}
