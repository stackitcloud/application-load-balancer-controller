package kubeutil

import corev1 "k8s.io/api/core/v1"

const (
	// From https://github.com/gardener/machine-controller-manager/blob/fc341881a5e71d7c5f240ca73415f967084aa85b/pkg/util/provider/machineutils/utils.go#L61
	ConditionNodeTermination corev1.NodeConditionType = "Terminating"
)

// GetNodeCondition return the condition or nil if no condition matching t was found.
func GetNodeCondition(node *corev1.Node, t corev1.NodeConditionType) *corev1.NodeCondition {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == t {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}
