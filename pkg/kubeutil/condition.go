package kubeutil

import corev1 "k8s.io/api/core/v1"

// GetNodeCondition return the condition or nil if no condition matching t was found.
func GetNodeCondition(node *corev1.Node, t corev1.NodeConditionType) *corev1.NodeCondition {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == t {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}
