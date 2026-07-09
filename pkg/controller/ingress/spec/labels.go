package spec

const (

	// prefixALBIngressController is the prefix for all labels associated with ingress controllers
	prefixALBIngressController = "alb-ingress-controller-"
	// LabelIngressClassUID is the unique key that identifies resources
	// owned by a specific IngressClass.
	LabelIngressClassUID = prefixALBIngressController + "ingress-class-uid"
)
