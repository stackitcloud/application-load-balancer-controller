package kubeutil

import corev1 "k8s.io/api/core/v1"

// GetTaint returns the taint or nil if not taint with the key was found.
func GetTaint(node *corev1.Node, key string) *corev1.Taint {
	for i := range node.Spec.Taints {
		if node.Spec.Taints[i].Key == key {
			return &node.Spec.Taints[i]
		}
	}
	return nil
}
