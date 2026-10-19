package operator

import (
	clienttesting "k8s.io/client-go/testing"
)

func hasDelete(acts []clienttesting.Action, name string) bool {
	for _, a := range acts {
		if d, ok := a.(clienttesting.DeleteAction); ok && d.GetName() == name {
			return true
		}
	}
	return false
}
