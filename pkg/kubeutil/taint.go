package kubeutil

import corev1 "k8s.io/api/core/v1"

const (
	// From https://github.com/kubernetes/cloud-provider/blob/81e4f58b4d1badd71d633d356faaaf69d971d874/controllers/service/controller.go#L64C2-L64C53
	TaintToBeDeleted = "ToBeDeletedByClusterAutoscaler"
)

// GetTaint returns the taint or nil if not taint with the key was found.
func GetTaint(node *corev1.Node, key string) *corev1.Taint {
	for i := range node.Spec.Taints {
		if node.Spec.Taints[i].Key == key {
			return &node.Spec.Taints[i]
		}
	}
	return nil
}
