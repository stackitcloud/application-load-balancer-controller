package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB source ACL", Label("integration", "source-acl"), func() {
	XIt("allows the runner IP, blocks it after an ACL change, and allows it again after restoring the ACL", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		runnerIP := discoverRunnerPublicIP(ctx)
		allowCIDR := toSingleHostCIDR(runnerIP)
		denyCIDR := "203.0.113.1/32"

		serviceName := fixture.createBackend(ctx, fixture.name("backend"), fixture.response("backend"))
		ingressClass := fixture.createIngressClass(ctx, map[string]string{
			annotationAllowedRanges: allowCIDR,
		})
		host := fixture.host("source-acl")
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

		updateIngressClass(ctx, ingressClass.Name, func(current *networkingv1.IngressClass) {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[annotationAllowedRanges] = denyCIDR
		})
		waitForRequestBlocked(ctx, address, initialHTTPPort, host, "/", albUpdateTimeout)

		updateIngressClass(ctx, ingressClass.Name, func(current *networkingv1.IngressClass) {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[annotationAllowedRanges] = allowCIDR
		})
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", fixture.response("backend"), albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
