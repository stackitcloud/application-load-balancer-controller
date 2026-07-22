package spec

import "maps"

const (

	// prefixALBIngressController is the prefix for all labels associated with ingress controllers
	prefixALBIngressController = "alb-ingress-controller-"
	// LabelIngressClassUID is the unique key that identifies resources
	// owned by a specific IngressClass.
	LabelIngressClassUID = prefixALBIngressController + "ingress-class-uid"
)

// MergeExtraLabels merges extraLabels into labels. If there are the same key is in both maps, the labels map will have priority.
func MergeExtraLabels(labels, extraLabels map[string]string) map[string]string {
	l := maps.Clone(extraLabels)
	if l == nil {
		l = map[string]string{}
	}
	maps.Copy(l, labels)
	return l
}
