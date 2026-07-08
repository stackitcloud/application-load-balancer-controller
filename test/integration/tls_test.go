package integration_test

import (
	"context"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	networkingv1 "k8s.io/api/networking/v1"
)

var _ = Describe("ALB TLS", Label("integration", "tls"), func() {
	It("rotates TLS certificates and supports https-only updates", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		host := fixture.host("tls")
		backend := fixture.createBackend(ctx, fixture.name("tls-backend"), fixture.response("tls"))
		certificateOne, privateKeyOne, fingerprintOne := generateSelfSignedCertificate(host)
		certificateTwo, privateKeyTwo, fingerprintTwo := generateSelfSignedCertificate(host)
		secret := fixture.createTLSSecret(ctx, fixture.name("tls-secret"), certificateOne, privateKeyOne)

		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("tls"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort:  strconv.Itoa(initialHTTPPort),
				annotationHTTPSPort: strconv.Itoa(customHTTPSPort),
			},
			TLSSecretName: secret.Name,
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: backend,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", fixture.response("tls"), albProvisionTimeout)
		waitForHTTPSResponse(ctx, address, customHTTPSPort, host, "/", fixture.response("tls"), fingerprintOne, albProvisionTimeout)

		updateTLSSecret(ctx, secret.Namespace, secret.Name, certificateTwo, privateKeyTwo)
		waitForHTTPSResponse(ctx, address, customHTTPSPort, host, "/", fixture.response("tls"), fingerprintTwo, albUpdateTimeout)

		updateIngress(ctx, ingress.Namespace, ingress.Name, func(current *networkingv1.Ingress) {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[annotationHTTPSOnly] = "true"
		})
		waitForHTTPSResponse(ctx, address, customHTTPSPort, host, "/", fixture.response("tls"), fingerprintTwo, albUpdateTimeout)
		waitForHTTPUnavailable(ctx, address, initialHTTPPort, host, "/", albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})

	It("keeps HTTP routing active until the TLS secret exists so ACME challenges can complete", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		host := fixture.host("tls-bootstrap")
		backendBody := fixture.response("tls-bootstrap")
		backend := fixture.createBackend(ctx, fixture.name("tls-bootstrap-backend"), backendBody)
		certificate, privateKey, fingerprint := generateSelfSignedCertificate(host)
		secretName := fixture.name("tls-bootstrap-secret")

		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("tls-bootstrap"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort:  strconv.Itoa(initialHTTPPort),
				annotationHTTPSPort: strconv.Itoa(customHTTPSPort),
			},
			TLSSecretName: secretName,
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: backend,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", backendBody, albProvisionTimeout)
		waitForHTTPSUnavailable(ctx, address, customHTTPSPort, host, "/", albProvisionTimeout)

		fixture.createTLSSecret(ctx, secretName, certificate, privateKey)
		waitForHTTPResponse(ctx, address, initialHTTPPort, host, "/", backendBody, albUpdateTimeout)
		waitForHTTPSResponse(ctx, address, customHTTPSPort, host, "/", backendBody, fingerprint, albUpdateTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})

	It("keeps an https-only ingress unavailable on HTTP while HTTPS remains available", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, nil)
		host := fixture.host("tls-only")
		backendBody := fixture.response("tls-only")
		backend := fixture.createBackend(ctx, fixture.name("tls-only-backend"), backendBody)
		certificate, privateKey, fingerprint := generateSelfSignedCertificate(host)
		secret := fixture.createTLSSecret(ctx, fixture.name("tls-only-secret"), certificate, privateKey)

		ingress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("tls-only"),
			Host: host,
			Annotations: map[string]string{
				annotationHTTPPort:  strconv.Itoa(initialHTTPPort),
				annotationHTTPSPort: strconv.Itoa(customHTTPSPort),
				annotationHTTPSOnly: "true",
			},
			TLSSecretName: secret.Name,
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: backend,
					ServicePort: backendServicePort,
				},
			},
		})

		address := waitForIngressAddress(ctx, ingress.Namespace, ingress.Name)
		waitForHTTPUnavailable(ctx, address, initialHTTPPort, host, "/", albProvisionTimeout)
		waitForHTTPSResponse(ctx, address, customHTTPSPort, host, "/", backendBody, fingerprint, albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})

	It("applies ingressclass https-only by default but allows an ingress to override it", func(ctx context.Context) {
		fixture := newIntegrationFixture()

		namespace := fixture.createNamespace(ctx)
		ingressClass := fixture.createIngressClass(ctx, map[string]string{
			annotationHTTPSOnly: "true",
		})

		inheritedHost := fixture.host("tls-class-inherited")
		inheritedBody := fixture.response("tls-class-inherited")
		inheritedBackend := fixture.createBackend(ctx, fixture.name("tls-class-inherited-backend"), inheritedBody)
		inheritedCertificate, inheritedPrivateKey, inheritedFingerprint := generateSelfSignedCertificate(inheritedHost)
		inheritedSecret := fixture.createTLSSecret(ctx, fixture.name("tls-class-inherited-secret"), inheritedCertificate, inheritedPrivateKey)

		inheritedIngress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("tls-class-inherited"),
			Host: inheritedHost,
			Annotations: map[string]string{
				annotationHTTPPort:  strconv.Itoa(initialHTTPPort),
				annotationHTTPSPort: strconv.Itoa(customHTTPSPort),
			},
			TLSSecretName: inheritedSecret.Name,
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: inheritedBackend,
					ServicePort: backendServicePort,
				},
			},
		})

		inheritedAddress := waitForIngressAddress(ctx, inheritedIngress.Namespace, inheritedIngress.Name)
		waitForHTTPUnavailable(ctx, inheritedAddress, initialHTTPPort, inheritedHost, "/", albProvisionTimeout)
		waitForHTTPSResponse(ctx, inheritedAddress, customHTTPSPort, inheritedHost, "/", inheritedBody, inheritedFingerprint, albProvisionTimeout)

		overrideHost := fixture.host("tls-class-override")
		overrideBody := fixture.response("tls-class-override")
		overrideBackend := fixture.createBackend(ctx, fixture.name("tls-class-override-backend"), overrideBody)
		overrideCertificate, overridePrivateKey, overrideFingerprint := generateSelfSignedCertificate(overrideHost)
		overrideSecret := fixture.createTLSSecret(ctx, fixture.name("tls-class-override-secret"), overrideCertificate, overridePrivateKey)

		overrideIngress := fixture.createIngress(ctx, ingressSpec{
			Name: fixture.name("tls-class-override"),
			Host: overrideHost,
			Annotations: map[string]string{
				annotationHTTPPort:  strconv.Itoa(initialHTTPPort),
				annotationHTTPSPort: strconv.Itoa(customHTTPSPort),
				annotationHTTPSOnly: "false",
			},
			TLSSecretName: overrideSecret.Name,
			Paths: []ingressPathSpec{
				{
					Path:        "/",
					PathType:    networkingv1.PathTypePrefix,
					ServiceName: overrideBackend,
					ServicePort: backendServicePort,
				},
			},
		})

		overrideAddress := waitForIngressAddress(ctx, overrideIngress.Namespace, overrideIngress.Name)
		waitForHTTPResponse(ctx, overrideAddress, initialHTTPPort, overrideHost, "/", overrideBody, albProvisionTimeout)
		waitForHTTPSResponse(ctx, overrideAddress, customHTTPSPort, overrideHost, "/", overrideBody, overrideFingerprint, albProvisionTimeout)
		waitForHTTPStatus(ctx, overrideAddress, initialHTTPPort, inheritedHost, "/", 404, albProvisionTimeout)
		waitForHTTPSResponse(ctx, overrideAddress, customHTTPSPort, inheritedHost, "/", inheritedBody, inheritedFingerprint, albProvisionTimeout)

		deleteObjectAndWait(ctx, ingressClass, resourceDeletionTimeout)
		deleteObjectAndWait(ctx, namespace, resourceDeletionTimeout)
	})
})
