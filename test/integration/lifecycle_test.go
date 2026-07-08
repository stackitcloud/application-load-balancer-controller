package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB lifecycle", Label("integration", "lifecycle"), func() {
	It("preserves the ALB when ingresses are recreated under the same class", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		serviceA := fixture.createBackend(ctx, fixture.name("backend-a"), fixture.response("backend-a"))
		hostA := fixture.host("lifecycle-a")
		ingressA := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("ingress-a"),
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

		addressA := waitForIngressAddress(ctx, ingressA.Namespace, ingressA.Name)
		waitForHTTPResponse(ctx, addressA, initialHTTPPort, hostA, "/", fixture.response("backend-a"), albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressA, resourceDeletionTimeout)

		serviceB := fixture.createBackend(ctx, fixture.name("backend-b"), fixture.response("backend-b"))
		hostB := fixture.host("lifecycle-b")
		ingressB := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("ingress-b"),
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

		addressB := waitForIngressAddress(ctx, ingressB.Namespace, ingressB.Name)
		Expect(addressB).To(Equal(addressA))
		waitForHTTPResponse(ctx, addressB, initialHTTPPort, hostB, "/", fixture.response("backend-b"), albProvisionTimeout)
		waitForHTTPStatus(ctx, addressB, initialHTTPPort, hostA, "/", 404, albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
