package integration_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB many routes", Label("integration", "routing", "many-routes"), func() {
	It("serves many host and path combinations through the same ALB", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)

		routeGroups := []struct {
			hostPrefix string
			paths      []struct {
				path     string
				pathType networkingv1.PathType
				key      string
			}
		}{
			{
				hostPrefix: "routes-a",
				paths: []struct {
					path     string
					pathType networkingv1.PathType
					key      string
				}{
					{path: "/", pathType: networkingv1.PathTypeExact, key: "root"},
					{path: "/docs", pathType: networkingv1.PathTypeExact, key: "docs-exact"},
					{path: "/docs", pathType: networkingv1.PathTypePrefix, key: "docs-prefix"},
					{path: "/api", pathType: networkingv1.PathTypePrefix, key: "api-prefix"},
				},
			},
			{
				hostPrefix: "routes-b",
				paths: []struct {
					path     string
					pathType networkingv1.PathType
					key      string
				}{
					{path: "/", pathType: networkingv1.PathTypeExact, key: "root"},
					{path: "/shop", pathType: networkingv1.PathTypeExact, key: "shop-exact"},
					{path: "/shop", pathType: networkingv1.PathTypePrefix, key: "shop-prefix"},
					{path: "/healthz", pathType: networkingv1.PathTypeImplementationSpecific, key: "healthz"},
				},
			},
			{
				hostPrefix: "routes-c",
				paths: []struct {
					path     string
					pathType networkingv1.PathType
					key      string
				}{
					{path: "/", pathType: networkingv1.PathTypeExact, key: "root"},
					{path: "/admin", pathType: networkingv1.PathTypeExact, key: "admin-exact"},
					{path: "/admin", pathType: networkingv1.PathTypePrefix, key: "admin-prefix"},
					{path: "/metrics", pathType: networkingv1.PathTypeImplementationSpecific, key: "metrics"},
				},
			},
		}

		type expectedRoute struct {
			host string
			path string
			body string
		}

		expectedRoutes := make([]expectedRoute, 0, 12)
		ingressAddresses := make([]string, 0, len(routeGroups))

		for _, group := range routeGroups {
			host := fixture.host(group.hostPrefix)
			paths := make([]ingressPathSpec, 0, len(group.paths))

			for _, route := range group.paths {
				backendName := fixture.name(fmt.Sprintf("%s-%s", group.hostPrefix, route.key))
				responseBody := fixture.response(fmt.Sprintf("%s-%s", group.hostPrefix, route.key))
				serviceName := fixture.createBackend(ctx, backendName, responseBody)

				paths = append(paths, ingressPathSpec{
					Path:        route.path,
					PathType:    route.pathType,
					ServiceName: serviceName,
					ServicePort: backendServicePort,
				})

				requestPath := route.path
				switch route.key {
				case "docs-prefix":
					requestPath = "/docs/reference"
				case "api-prefix":
					requestPath = "/api/v1/items"
				case "shop-prefix":
					requestPath = "/shop/cart"
				case "admin-prefix":
					requestPath = "/admin/users"
				}

				expectedRoutes = append(expectedRoutes, expectedRoute{
					host: host,
					path: requestPath,
					body: responseBody,
				})
			}

			ingress := fixture.createIngress(ctx, ingressSpec{
				Name: fixture.name(group.hostPrefix),
				Host: host,
				Annotations: map[string]string{
					annotationHTTPPort: "8080",
				},
				Paths: paths,
			})
			ingressAddresses = append(ingressAddresses, waitForIngressAddress(ctx, ingress.Namespace, ingress.Name))
		}

		Expect(ingressAddresses).NotTo(BeEmpty())
		for _, address := range ingressAddresses[1:] {
			Expect(address).To(Equal(ingressAddresses[0]))
		}

		for _, route := range expectedRoutes {
			waitForHTTPResponse(ctx, ingressAddresses[0], initialHTTPPort, route.host, route.path, route.body, albProvisionTimeout)
		}

		waitForHTTPStatus(ctx, ingressAddresses[0], initialHTTPPort, fixture.host("routes-a"), "/missing", 404, albProvisionTimeout)
		waitForHTTPStatus(ctx, ingressAddresses[0], initialHTTPPort, fixture.host("unknown"), "/", 404, albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})

	It("keeps the first 20 path-backed routes serving traffic, ignores the 21st, and still reconciles updates to admitted routes", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)

		const supportedRouteCount = 20
		const updatedIndex = 0

		type routeSpec struct {
			path string
			body string
		}

		host := fixture.host("many-routes")
		routes := make([]routeSpec, 0, supportedRouteCount+1)
		paths := make([]ingressPathSpec, 0, supportedRouteCount+1)

		for index := 0; index <= supportedRouteCount; index++ {
			key := fmt.Sprintf("route-%03d", index)
			path := "/" + key
			body := fixture.response(key)
			serviceName := fixture.createBackend(ctx, fixture.name(key), body)

			routes = append(routes, routeSpec{
				path: path,
				body: body,
			})
			paths = append(paths, ingressPathSpec{
				Path:        path,
				PathType:    networkingv1.PathTypeExact,
				ServiceName: serviceName,
				ServicePort: backendServicePort,
			})
		}

		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("many-routes"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort: "8080",
			},
			Paths: paths,
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)

		for _, route := range routes[:supportedRouteCount] {
			waitForHTTPResponse(ctx, address, initialHTTPPort, host, route.path, route.body, albProvisionTimeout)
		}

		overflow := routes[supportedRouteCount]
		waitForHTTPUnavailable(ctx, address, initialHTTPPort, host, overflow.path, albProvisionTimeout)

		updatedBody := fixture.response("route-000-updated")
		updatedService := fixture.createBackend(ctx, fixture.name("route-000-updated"), updatedBody)
		updateIngress(ctx, namespace.Name, ingress.Name, func(current *networkingv1.Ingress) {
			current.Spec.Rules[0].HTTP.Paths[updatedIndex].Backend.Service.Name = updatedService
		})
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, routes[updatedIndex].path, updatedBody, albUpdateTimeout)

		stillAdmitted := routes[supportedRouteCount-1]
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, stillAdmitted.path, stillAdmitted.body, albUpdateTimeout)
		waitForHTTPUnavailable(ctx, address, initialHTTPPort, host, overflow.path, albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
