package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB routing", Label("integration", "routing"), func() {
	It("routes multiple hosts through the same ALB and keeps the remaining host working after one ingress is deleted", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		serviceA := fixture.createBackend(ctx, fixture.name("host-a"), fixture.response("host-a"))
		serviceB := fixture.createBackend(ctx, fixture.name("host-b"), fixture.response("host-b"))
		hostA := fixture.host("host-a")
		hostB := fixture.host("host-b")

		ingressA := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("host-a"),
			Host: hostA,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: serviceA,
					ServicePort: backendServicePort,
				},
			},
		})
		ingressB := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("host-b"),
			Host: hostB,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: serviceB,
					ServicePort: backendServicePort,
				},
			},
		})

		addressA := waitForIngressAddress(ctx, ingressA.Namespace, ingressA.Name)
		addressB := waitForIngressAddress(ctx, ingressB.Namespace, ingressB.Name)
		Expect(addressA).To(Equal(addressB))

		waitForHTTPResponse(ctx, addressA, initialHTTPPort, hostA, "/", fixture.response("host-a"), albProvisionTimeout)
		waitForHTTPResponse(ctx, addressB, initialHTTPPort, hostB, "/", fixture.response("host-b"), albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressA, resourceDeletionTimeout)

		waitForHTTPResponse(ctx, addressB, initialHTTPPort, hostB, "/", fixture.response("host-b"), albUpdateTimeout)
		waitForHTTPStatus(ctx, addressB, initialHTTPPort, hostA, "/", 404, albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})

	It("prefers exact and implementation-specific matches over prefix routes", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		host := fixture.host("routing")
		exactService := fixture.createBackend(ctx, fixture.name("exact"), fixture.response("exact"))
		prefixService := fixture.createBackend(ctx, fixture.name("prefix"), fixture.response("prefix"))
		implementationSpecificService := fixture.createBackend(ctx, fixture.name("impl"), fixture.response("impl"))

		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("routing"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: []ingressPathSpec{
				{
					Path:        "/app",
					PathType:    networkingv1.PathTypeExact,
					ServiceName: exactService,
					ServicePort: backendServicePort,
				},
				{
					Path:        "/app",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: prefixService,
					ServicePort: backendServicePort,
				},
				{
					Path:        "/impl",
					PathType:    networkingv1.PathTypeImplementationSpecific,
					ServiceName: implementationSpecificService,
					ServicePort: backendServicePort,
				},
				{
					Path:        "/impl",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: prefixService,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/app", fixture.response("exact"), albProvisionTimeout)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/app/child", fixture.response("prefix"), albProvisionTimeout)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/impl", fixture.response("impl"), albProvisionTimeout)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/impl/child", fixture.response("prefix"), albProvisionTimeout)
		waitForHTTPStatus(ctx, address, initialHTTPPort, host, "/missing", 404, albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
