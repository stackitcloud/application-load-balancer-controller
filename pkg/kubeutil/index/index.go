package index

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// FieldIndexIngressClass indexes the ingress class on an ingress.
	FieldIndexIngressClass = ".spec.ingressClassName"
	// FieldIndexService indexes all service references on an ingress. An ingress can be indexed multiple times.
	FieldIndexService = ".spec.rules.http.paths.backend.service.name"
	// FieldIndexSecret indexes all secret references on an ingress. An ingress can be indexed multiple times.
	FieldIndexSecret = ".spec.tls.secret" //nolint:gosec // field index key, not a credential

)

func IngressClass(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.GetCache().IndexField(ctx, &networkingv1.Ingress{}, FieldIndexIngressClass, func(o client.Object) []string {
		ingress := o.(*networkingv1.Ingress)
		if ingress.Spec.IngressClassName == nil {
			return nil
		}
		return []string{*ingress.Spec.IngressClassName}
	}); err != nil {
		return fmt.Errorf("failed to index ingress class on ingresses: %w", err)
	}
	return nil
}

func IngressServiceBackend(ctx context.Context, mgr manager.Manager) error {

	if err := mgr.GetCache().IndexField(ctx, &networkingv1.Ingress{}, FieldIndexService, func(o client.Object) []string {
		ingress := o.(*networkingv1.Ingress)
		refs := []string{}
		if ingress.Spec.DefaultBackend != nil && ingress.Spec.DefaultBackend.Service != nil && ingress.Spec.DefaultBackend.Service.Name != "" {
			refs = append(refs, ingress.Spec.DefaultBackend.Service.Name)
		}
		for i := range ingress.Spec.Rules {
			rule := &ingress.Spec.Rules[i]
			if rule.HTTP == nil {
				continue
			}
			for j := range rule.HTTP.Paths {
				path := &rule.HTTP.Paths[j]
				if path.Backend.Service != nil && path.Backend.Service.Name != "" {
					refs = append(refs, path.Backend.Service.Name)
				}
			}
		}
		return refs
	}); err != nil {
		return fmt.Errorf("failed to index services on ingresses: %w", err)
	}
	return nil
}

func IngressSecret(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.GetCache().IndexField(ctx, &networkingv1.Ingress{}, FieldIndexSecret, func(o client.Object) []string {
		ingress := o.(*networkingv1.Ingress)
		refs := []string{}
		for i := range ingress.Spec.TLS {
			refs = append(refs, ingress.Spec.TLS[i].SecretName)
		}
		return refs
	}); err != nil {
		return fmt.Errorf("failed to index secrets on ingresses: %w", err)
	}
	return nil
}
