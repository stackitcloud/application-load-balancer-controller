package targets

import (
	"context"
	"errors"
	"fmt"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/kubeutil/index"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ Retriever = (*PodIPRetriever)(nil)

type PodIPRetriever struct {
	Client         client.Client
	ControllerName string
}

// SetupWithController implements [ControllerRetriever].
func (r *PodIPRetriever) SetupWithController(b *builder.Builder) {
	// TODO: predicate
	b.Watches(&discoveryv1.EndpointSlice{}, r.endpointSliceEventHandler(r.Client))
}

// Targets implements [Retriever].
func (r *PodIPRetriever) Targets(
	ctx context.Context, _ *networkingv1.IngressClass, ing *networkingv1.Ingress, backend *networkingv1.IngressBackend,
) ([]albsdk.Target, error) {
	if backend.Service == nil {
		return nil, errors.New("backend service is empty")
	}
	endpointSlices, err := r.endpointSlicesForService(ctx, backend.Service.Name, ing.Namespace)
	if err != nil {
		return nil, err
	}

	targets := []albsdk.Target{}
	for _, endpointSlice := range endpointSlices {
		for _, endpoint := range endpointSlice.Endpoints {
			// filter endpoints that are not yet ready to accept traffic
			if !ptr.Deref(endpoint.Conditions.Ready, true) || !ptr.Deref(endpoint.Conditions.Serving, true) {
				continue
			}
			for _, addr := range endpoint.Addresses {
				targets = append(targets, albsdk.Target{
					DisplayName: new(displayName(endpoint)),
					Ip:          new(addr),
				})
			}
		}
	}
	return targets, nil
}

// Port implements [Retriever].
func (r *PodIPRetriever) Port(
	ctx context.Context, svc *corev1.Service, ingServiceBackend *networkingv1.IngressServiceBackend,
) (int32, error) {
	endpointSlices, err := r.endpointSlicesForService(ctx, svc.Name, svc.Namespace)
	if err != nil {
		return 0, err
	}

	logger := log.FromContext(ctx).WithValues("service", client.ObjectKeyFromObject(svc))

	endpointPorts := []discoveryv1.EndpointPort{}
	for i := range endpointSlices {
		endpointPorts = append(endpointPorts, endpointSlices[i].Ports...)
	}
	logger.V(1).Info("checking endpoint slice ports", "ports", endpointPorts)

	targetPort := int32(0)
	for _, port := range endpointPorts {
		if port.Port == nil {
			continue
		}
		// We must not match an empty port name against an empty port name.
		if *port.Port == ingServiceBackend.Port.Number ||
			(ptr.Deref(port.Name, "") != "" && ptr.Deref(port.Name, "") == ingServiceBackend.Port.Name) {
			targetPort = *port.Port
		}
	}
	if targetPort == 0 {
		return 0, errors.New("Port not found in service")
	}
	return targetPort, nil
}

func (r *PodIPRetriever) endpointSlicesForService(ctx context.Context, svcName, namespace string) ([]discoveryv1.EndpointSlice, error) {
	esList := &discoveryv1.EndpointSliceList{}
	if err := r.Client.List(ctx, esList, client.InNamespace(namespace), client.MatchingLabels{
		discoveryv1.LabelServiceName: svcName,
	}); err != nil {
		return nil, err
	}
	return esList.Items, nil
}

func (r *PodIPRetriever) endpointSliceEventHandler(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		es, ok := obj.(*discoveryv1.EndpointSlice)
		if !ok {
			return nil
		}

		svcName, ok := es.Labels[discoveryv1.LabelServiceName]
		if !ok {
			return nil
		}
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: es.Namespace,
			},
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
			log.FromContext(ctx).Error(err, "retrieving service")
			return nil
		}

		ingressList := &networkingv1.IngressList{}
		if err := c.List(ctx, ingressList, client.MatchingFields(fields.Set{
			index.FieldIndexService: svcName,
		})); err != nil {
			log.FromContext(ctx).Error(err, "retrieving ingresses matching service", "service", client.ObjectKeyFromObject(svc))
			return nil
		}

		ingressClasses := sets.New[string]()
		for _, ing := range ingressList.Items {
			if ing.Spec.IngressClassName != nil {
				ingressClasses = ingressClasses.Insert(*ing.Spec.IngressClassName)
			}
		}

		requestList := []ctrl.Request{}
		for _, ingClassName := range ingressClasses.UnsortedList() {
			ingClass := &networkingv1.IngressClass{}
			if err := c.Get(ctx, types.NamespacedName{Name: ingClassName}, ingClass); err != nil {
				log.FromContext(ctx).Error(err, "retrieving ingressClass")
				return nil

			}
			if ingClass.Spec.Controller != r.ControllerName {
				continue
			}
			requestList = append(requestList, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(ingClass),
			})
		}
		return requestList
	})
}

func displayName(endpoint discoveryv1.Endpoint) string {
	name := fmt.Sprintf("%s-%s-%s", endpoint.TargetRef.Kind, endpoint.TargetRef.Name, endpoint.TargetRef.Namespace)
	if len(name) >= 63 {
		return name[:63]
	}
	return name
}
