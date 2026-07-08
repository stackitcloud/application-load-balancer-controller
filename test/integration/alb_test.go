package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB controller integration", Label("integration"), func() {
	It("creates an ALB, updates its config, and deletes it", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		serviceName := fixture.createBackend(ctx, fixture.name("backend"), fixture.response("backend"))
		ingressClass := fixture.createIngressClass(ctx, nil)
		host := fixture.host("basic")
		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("ingress"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: serviceName,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", fixture.response("backend"), albProvisionTimeout)

		updateIngressHTTPPort(ctx, ingress.Namespace, ingress.Name, updatedHTTPPort)
		waitForHTTPResponse(ctx, address, updatedHTTPPort, host, "/", fixture.response("backend"), albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})

	It("creates an ALB with a custom HTTP port and deletes it", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		serviceName := fixture.createBackend(ctx, fixture.name("backend"), fixture.response("backend"))
		ingressClass := fixture.createIngressClass(ctx, nil)
		host := fixture.host("custom-port")
		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("ingress"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: serviceName,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", fixture.response("backend"), albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
