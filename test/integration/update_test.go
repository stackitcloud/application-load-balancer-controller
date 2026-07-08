package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB updates", Label("integration", "updates"), func() {
	It("reconciles backend, host, and path changes on an existing ingress", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		backendA := fixture.createBackend(ctx, fixture.name("backend-a"), fixture.response("backend-a"))
		backendB := fixture.createBackend(ctx, fixture.name("backend-b"), fixture.response("backend-b"))
		hostA := fixture.host("update-a")
		hostB := fixture.host("update-b")

		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("updates"),
			Host: hostA,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: []ingressPathSpec{
				{
					Path:        "/app",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: backendA,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPResponse(ctx, address, initialHTTPPort, hostA, "/app", fixture.response("backend-a"), albProvisionTimeout)

		updateIngress(ctx, ingress.Namespace, ingress.Name, func(current *networkingv1.Ingress) {
			current.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = backendB
		})
		waitForHTTPResponse(ctx, address, initialHTTPPort, hostA, "/app", fixture.response("backend-b"), albUpdateTimeout)

		updateIngress(ctx, ingress.Namespace, ingress.Name, func(current *networkingv1.Ingress) {
			current.Spec.Rules[0].Host = hostB
		})
		waitForHTTPResponse(ctx, address, initialHTTPPort, hostB, "/app", fixture.response("backend-b"), albUpdateTimeout)
		waitForHTTPStatus(ctx, address, initialHTTPPort, hostA, "/app", 404, albUpdateTimeout)

		updateIngress(ctx, ingress.Namespace, ingress.Name, func(current *networkingv1.Ingress) {
			current.Spec.Rules[0].HTTP.Paths[0].Path = "/v2"
		})
		waitForHTTPResponse(ctx, address, initialHTTPPort, hostB, "/v2", fixture.response("backend-b"), albUpdateTimeout)
		waitForHTTPStatus(ctx, address, initialHTTPPort, hostB, "/app", 404, albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
