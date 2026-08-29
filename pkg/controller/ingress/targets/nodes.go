package targets

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/kubeutil"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var _ Retriever = (*NodeRetriever)(nil)

type NodeRetriever struct {
	Client             client.Client
	TargetPerPoolLimit int
	ControllerName     string
}

// Port implements [Retriever].
func (r *NodeRetriever) Port(service *corev1.Service, ingServiceBackend *networkingv1.IngressServiceBackend) (int32, error) {
	if service.Spec.Type != corev1.ServiceTypeNodePort && service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return 0, errors.New("service is not of type NodePort or LoadBalancer")
	}
	nodePort := int32(0)
	for _, port := range service.Spec.Ports {
		// We must not match an empty port name against an empty port name.
		if port.Port == ingServiceBackend.Port.Number ||
			(port.Name != "" && port.Name == ingServiceBackend.Port.Name) {
			if port.NodePort == 0 {
				return 0, errors.New("Service port doesn't have a node port")
			}
			nodePort = port.NodePort
		}
	}
	if nodePort == 0 {
		return 0, errors.New("Port not found in service")
	}
	return nodePort, nil
}

func (r *NodeRetriever) SetupWithController(b *builder.Builder) {
	b.Watches(&corev1.Node{}, r.nodeEventHandler(r.Client), builder.WithPredicates(nodePredicate()))
}

func (r *NodeRetriever) Targets(ctx context.Context, _ *networkingv1.IngressClass, _ *networkingv1.Ingress) ([]albsdk.Target, error) {
	nodeList := corev1.NodeList{}
	if err := r.Client.List(ctx, &nodeList); err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}
	return r.getTargetsOfNodes(nodeList.Items), nil
}

// getTargetsOfNodes returns all targets that should be used for the application load balancer.
// It filters out nodes that don't qualify as targets.
// The returned slice is sorted.
func (r *NodeRetriever) getTargetsOfNodes(nodes []corev1.Node) []albsdk.Target {
	slices.SortFunc(nodes, func(a, b corev1.Node) int {
		return b.CreationTimestamp.Compare(a.CreationTimestamp.Time)
	})

	targets := []albsdk.Target{}
	for i := range nodes {
		node := &nodes[i]
		if isNodeTerminating(node) {
			continue
		}
		for j := range node.Status.Addresses {
			address := node.Status.Addresses[j]
			if address.Type == corev1.NodeInternalIP {
				targets = append(targets, albsdk.Target{
					DisplayName: &node.Name, // TODO: Sanitize node name (see CCM)
					Ip:          &address.Address,
				})
				break
			}
		}
		if len(targets) >= r.targetPerPoolLimit {
			break
		}
	}
	slices.SortFunc(targets, func(a, b albsdk.Target) int {
		return cmp.Compare(*a.Ip, *b.Ip)
	})
	return targets
}

func isNodeTerminating(node *corev1.Node) bool {
	if kubeutil.GetTaint(node, kubeutil.TaintToBeDeleted) != nil {
		return true
	}
	if cond := kubeutil.GetNodeCondition(node, kubeutil.ConditionNodeTermination); cond != nil && cond.Status == corev1.ConditionTrue {
		return true
	}
	return false
}

func (r *NodeRetriever) nodeEventHandler(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
		ingressClassList := &networkingv1.IngressClassList{}
		err := c.List(ctx, ingressClassList)
		if err != nil {
			return nil
		}
		requestList := []ctrl.Request{}
		for i := range ingressClassList.Items {
			if ingressClassList.Items[i].Spec.Controller != r.controllerName {
				continue
			}
			requestList = append(requestList, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&ingressClassList.Items[i]),
			})
		}
		return requestList
	})
}

func nodePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, ok := e.ObjectOld.(*corev1.Node)
			if !ok {
				return false
			}
			newNode, ok := e.ObjectNew.(*corev1.Node)
			if !ok {
				return false
			}

			return !reflect.DeepEqual(oldNode.Status.Addresses, newNode.Status.Addresses) ||
				!reflect.DeepEqual(kubeutil.GetTaint(oldNode, kubeutil.TaintToBeDeleted), kubeutil.GetTaint(newNode, kubeutil.TaintToBeDeleted)) ||
				!reflect.DeepEqual(kubeutil.GetNodeCondition(oldNode, kubeutil.ConditionNodeTermination), kubeutil.GetNodeCondition(newNode, kubeutil.ConditionNodeTermination))
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return true
		},
		GenericFunc: func(_ event.GenericEvent) bool {
			return true
		},
	}
}
