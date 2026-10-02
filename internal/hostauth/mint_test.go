package hostauth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestMintKubeconfig_Success(t *testing.T) {
	clientset := fake.NewClientset()
	var requestedNamespace, requestedName string
	clientset.PrependReactor("create", "serviceaccounts", func(action ktesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(ktesting.CreateActionImpl)
		if !ok || createAction.GetSubresource() != "token" {
			return false, nil, nil
		}
		requestedNamespace = createAction.GetNamespace()
		requestedName = createAction.Name
		return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{Token: "minted-token"}}, nil
	})

	kc, err := MintKubeconfig(context.Background(), clientset, "hyve-system", "hyve-host-admin", "https://kubernetes.default.svc", []byte("fake-ca-data"), time.Hour)
	require.NoError(t, err)

	assert.Equal(t, "hyve-system", requestedNamespace)
	assert.Equal(t, "hyve-host-admin", requestedName)
	kcStr := string(kc)
	assert.Contains(t, kcStr, "minted-token")
	assert.Contains(t, kcStr, "https://kubernetes.default.svc")
}

func TestMintKubeconfig_TokenRequestError_Propagates(t *testing.T) {
	clientset := fake.NewClientset()
	clientset.PrependReactor("create", "serviceaccounts", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, assert.AnError
	})

	_, err := MintKubeconfig(context.Background(), clientset, "hyve-system", "hyve-host-admin", "https://kubernetes.default.svc", nil, time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mint token for hyve-system/hyve-host-admin")
}
