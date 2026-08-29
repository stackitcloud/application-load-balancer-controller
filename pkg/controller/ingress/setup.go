package ingress

import (
	"context"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/targets"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/kubeutil/index"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SetupWithManager sets up the controller with the Manager.
func (r *IngressClassReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, ctrlName string) error {
	for _, indexer := range []func(context.Context, manager.Manager) error{
		index.IngressClass,
		index.IngressSecret,
		index.IngressServiceBackend,
	} {
		if err := indexer(ctx, mgr); err != nil {
			return err
		}
	}

	if ctrlName == "" {
		ctrlName = "ingressclass"
	}

	b := ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.IngressClass{}, builder.WithPredicates(ingressClassPredicate())).
		Watches(&networkingv1.Ingress{}, ingressEventHandler(r.Client)).
		Watches(&corev1.Secret{}, secretEventHandler(r.Client)).
		Watches(&corev1.Service{}, serviceEventHandler(r.Client)).
		Named(ctrlName)

	for _, retriever := range r.TargetRetrievers {
		if cr, ok := retriever.(targets.ControllerRetriever); ok {
			cr.SetupWithController(b)
		}
	}

	return b.Complete(r)
}

// secretEventHandler returns all ingress classes that have at least one ingress that references the given secret.
func secretEventHandler(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
		// Filter out non-TLS Secrets.
		secret, ok := o.(*corev1.Secret)
		if !ok || secret.Type != corev1.SecretTypeTLS {
			return nil
		}
		return ingressClassRequestsForReferencingIngresses(ctx, c, secret.Namespace, index.FieldIndexSecret, secret.Name)
	})
}

// serviceEventHandler returns all ingress classes that have at least one ingress that references the given service.
func serviceEventHandler(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
		service, ok := o.(*corev1.Service)
		if !ok {
			return nil
		}
		return ingressClassRequestsForReferencingIngresses(ctx, c, service.Namespace, index.FieldIndexService, service.Name)
	})
}

// ingressClassRequestsForReferencingIngresses lists all ingresses in the given namespace that reference an object
// (identified via the provided field index and value) and returns reconcile requests for all unique ALB-controlled
// ingress classes those ingresses belong to.
func ingressClassRequestsForReferencingIngresses(ctx context.Context, c client.Client, namespace, fieldIndex, value string) []ctrl.Request {
	ingresses := &networkingv1.IngressList{}
	if err := c.List(ctx, ingresses, client.InNamespace(namespace), client.MatchingFields{fieldIndex: value}); err != nil {
		return nil
	}

	classes := map[string]any{}
	for i := range ingresses.Items {
		ingress := &ingresses.Items[i]
		if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName != "" {
			classes[*ingress.Spec.IngressClassName] = nil
		}
	}

	reqs := []ctrl.Request{}
	for className := range classes {
		class := &networkingv1.IngressClass{}
		if err := c.Get(ctx, types.NamespacedName{Name: className}, class); err != nil {
			continue
		}
		if class.Spec.Controller == ControllerName {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: className}})
		}
	}
	return reqs
}

func ingressEventHandler(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
		ingress, ok := o.(*networkingv1.Ingress)
		if !ok || ingress.Spec.IngressClassName == nil {
			return nil
		}

		ingressClass := &networkingv1.IngressClass{}
		err := c.Get(ctx, client.ObjectKey{Name: *ingress.Spec.IngressClassName}, ingressClass)
		if err != nil {
			return nil
		}

		if ingressClass.Spec.Controller != ControllerName {
			return nil
		}

		return []ctrl.Request{
			{
				NamespacedName: client.ObjectKeyFromObject(ingressClass),
			},
		}
	})
}

func ingressClassPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		ingressClass, ok := object.(*networkingv1.IngressClass)
		if !ok {
			return false
		}
		return ingressClass.Spec.Controller == ControllerName
	})
}
