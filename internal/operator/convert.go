package operator

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func conditionToMap(c metav1.Condition) (map[string]any, error) {
	return runtime.DefaultUnstructuredConverter.ToUnstructured(&c)
}

func conditionFromMap(m map[string]any) (metav1.Condition, error) {
	var c metav1.Condition
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &c)
	return c, err
}
