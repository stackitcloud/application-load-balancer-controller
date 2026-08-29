package targets

import (
	"context"

	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
)

type Retriever interface {
	Targets(context.Context, *networkingv1.IngressClass, *networkingv1.Ingress, *networkingv1.IngressBackend) ([]albsdk.Target, error)
	Port(context.Context, *corev1.Service, *networkingv1.IngressServiceBackend) (int32, error)
}

// ControllerRetriever is a Retriever that needs to be setup with a controller.
type ControllerRetriever interface {
	Retriever
	SetupWithController(b *builder.Builder)
}
