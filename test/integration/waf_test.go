package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB WAF", Label("integration", "waf"), func() {
	It("attaches a STACKIT WAF to the ALB and blocks matching requests", func(ctx context.Context) {
		serviceAccountKey := stackitServiceAccountKeyOrSkip()
		cfg := newStackitTestConfigFromCluster(ctx)
		fixture := newIntegrationFixture()
		wafName := createTestWAF(ctx, cfg, serviceAccountKey, fixture)

		namespace := fixture.createNamespace(ctx)
		serviceName := fixture.createBackend(ctx, fixture.name("backend"), fixture.response("backend"))
		ingressClass := fixture.createIngressClass(ctx, map[string]string{
			annotationWAFName: wafName,
		})
		host := fixture.host("waf")
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
		waitForALBListenerWAF(ctx, cfg, serviceAccountKey, ingressClass, initialHTTPPort, wafName)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", fixture.response("backend"), albProvisionTimeout)
		waitForRequestBlocked(ctx, address, initialHTTPPort, host, "/blocked-by-waf", albUpdateTimeout)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", fixture.response("backend"), albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		waitForALBDeletion(ctx, cfg, serviceAccountKey, ingressClass)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
