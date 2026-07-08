package integration_test

import (
	"context"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB many listeners", Label("integration", "listeners", "many-listeners"), func() {
	XIt("requires controller support to isolate the 20-listener limit from the 20-target-pool limit", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)

		const supportedListenerCount = 20

		type listenerRoute struct {
			port           int
			host           string
			body           string
			address        string
			name           string
			updatedBody    string
			updatedService string
		}

		listeners := make([]listenerRoute, 0, supportedListenerCount+1)

		for offset := 0; offset <= supportedListenerCount; offset++ {
			port := initialHTTPPort + offset
			host := fixture.host("listener-" + strconv.Itoa(port))
			body := fixture.response("listener-" + strconv.Itoa(port))
			updatedBody := fixture.response("listener-" + strconv.Itoa(port) + "-updated")
			name := fixture.name("listener-" + strconv.Itoa(port))
			serviceName := fixture.createBackend(ctx, name, body)
			updatedService := fixture.createBackend(ctx, name+"-updated", updatedBody)

			ingress := fixture.createIngress(ctx, ingressSpec{
				Name: name,
				Host: host,
				Annotations: map[string]string{
					annotationHTTPPort: strconv.Itoa(port),
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

			listeners = append(listeners, listenerRoute{
				port:           port,
				host:           host,
				body:           body,
				address:        waitForIngressAddress(ctx, ingress.Namespace, ingress.Name),
				name:           ingress.Name,
				updatedBody:    updatedBody,
				updatedService: updatedService,
			})
		}

		Expect(listeners).NotTo(BeEmpty())
		for _, listener := range listeners[1:] {
			Expect(listener.address).To(Equal(listeners[0].address))
		}

		for _, listener := range listeners[:supportedListenerCount] {
			waitForHTTPResponse(ctx, listener.address, listener.port, listener.host, "/", listener.body, albProvisionTimeout)
		}

		overflow := listeners[supportedListenerCount]
		waitForHTTPUnavailable(ctx, listeners[0].address, overflow.port, overflow.host, "/", albProvisionTimeout)

		for index, listener := range listeners[:supportedListenerCount] {
			otherHost := listeners[(index+1)%supportedListenerCount].host
			waitForHTTPStatus(ctx, listener.address, listener.port, otherHost, "/", 404, albProvisionTimeout)
		}

		updated := listeners[0]
		updateIngress(ctx, namespace.Name, updated.name, func(current *networkingv1.Ingress) {
			current.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = updated.updatedService
		})
		waitForHTTPResponse(ctx, updated.address, updated.port, updated.host, "/", updated.updatedBody, albUpdateTimeout)

		stillAdmitted := listeners[supportedListenerCount-1]
		waitForHTTPResponse(ctx, stillAdmitted.address, stillAdmitted.port, stillAdmitted.host, "/", stillAdmitted.body, albUpdateTimeout)
		waitForHTTPUnavailable(ctx, listeners[0].address, overflow.port, overflow.host, "/", albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
