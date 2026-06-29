package spec

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ErrorEvent struct {
	Ingress     client.Object
	Description string
	FieldPath   *field.Path
}

func (e *ErrorEvent) Error() string {
	if e.FieldPath != nil {
		return fmt.Sprintf("%s: %s", e.FieldPath.String(), e.Description)
	}
	return e.Description
}

func (e *ErrorEvent) RecordEvent(class *networkingv1.IngressClass, recorder record.EventRecorder) {
	recorder.Eventf(class, corev1.EventTypeWarning, "IngressWarning", "Error in %s in Namespace %s: %s", e.Ingress.GetName(), e.Ingress.GetNamespace(), e.Error())
	recorder.Event(e.Ingress, corev1.EventTypeWarning, "IngressWarning", e.Error())
}
