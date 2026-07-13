package spec

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/onsi/gomega/types"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec/testdata"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/testutil"
	. "github.com/stackitcloud/application-load-balancer-controller/pkg/testutil/ingress"
	. "github.com/stackitcloud/application-load-balancer-controller/pkg/testutil/service"
	"github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var _ = Describe("WorkTreeALB", func() {
	It("should sort rules from most to least-specific even if their priority is inversed", func() {
		tree, errs, err := BuildTree(&networkingv1.IngressClass{}, []networkingv1.Ingress{
			Ingress(
				"default", "ingress-with-higher-priority",
				WithAnnotation(AnnotationPriority, "5"),
				WithRule("my-host.local",
					WithPath("/prefix/b", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
					WithPath("/exact/b", new(networkingv1.PathTypeExact), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
					WithPath("/exact/b/b", new(networkingv1.PathTypeExact), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
				),
			),
			Ingress(
				"default", "ingress-with-lower-priority",
				WithAnnotation(AnnotationPriority, "4"),
				WithRule("my-host.local",
					WithPath("/prefix/a", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
					WithPath("/exact/a", new(networkingv1.PathTypeExact), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
					WithPath("/exact/a/a", new(networkingv1.PathTypeExact), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
				),
			),
			Ingress(
				"default", "ingress-with-default-priority",
				WithRule("my-host.local",
					WithPath("/implementation-specific", new(networkingv1.PathTypeImplementationSpecific), "my-service", networkingv1.ServiceBackendPort{Number: 1337}),
				),
			),
		}, nil, []corev1.Service{
			Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 1337, 30000, corev1.ProtocolTCP)),
		}, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(errs).To(BeEmpty())
		createPayload := tree.ToCreatePayload(nil, "", "")
		Expect(createPayload.Listeners[0].Http.Hosts[0].Host).To(HaveValue(Equal("my-host.local")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules).To(HaveLen(7))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[0].Path.ExactMatch).To(HaveValue(Equal("/exact/a/a")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[1].Path.ExactMatch).To(HaveValue(Equal("/exact/b/b")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[2].Path.ExactMatch).To(HaveValue(Equal("/exact/a")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[3].Path.ExactMatch).To(HaveValue(Equal("/exact/b")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[4].Path.ExactMatch).To(HaveValue(Equal("/implementation-specific")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[5].Path.Prefix).To(HaveValue(Equal("/prefix/a")))
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[6].Path.Prefix).To(HaveValue(Equal("/prefix/b")))
	})

	It("should match rules against correct node ports", func() {
		const host = "my-host.local"
		tree, errs, err := BuildTree(&networkingv1.IngressClass{}, []networkingv1.Ingress{
			Ingress(
				"default", "ingress-to-node-port-5000", WithUID("uid-1"),
				WithRule(host, WithPath("/5000", new(networkingv1.PathTypeExact), "service-a", networkingv1.ServiceBackendPort{Number: 1337})),
			),
			Ingress(
				"default", "ingress-to-node-port-5001", WithUID("uid-2"),
				WithRule(host, WithPath("/5001", new(networkingv1.PathTypeExact), "service-a", networkingv1.ServiceBackendPort{Name: "1338"})),
			),
			Ingress(
				"default", "ingress-to-node-port-5002", WithUID("uid-3"),
				WithRule(host, WithPath("/5002", new(networkingv1.PathTypeExact), "service-a", networkingv1.ServiceBackendPort{Number: 1339})),
			),
			Ingress(
				"default", "ingress-to-node-port-5003", WithUID("uid-4"),
				WithRule(host, WithPath("/5003", new(networkingv1.PathTypeExact), "service-b", networkingv1.ServiceBackendPort{Number: 1337})),
			),
		}, nil, []corev1.Service{
			Service("default", "service-a", WithServiceType(corev1.ServiceTypeNodePort),
				WithPort("1337", 1337, 5000, corev1.ProtocolTCP),
				WithPort("1338", 1338, 5001, corev1.ProtocolTCP),
				WithPort("1339", 1339, 5002, corev1.ProtocolTCP),
			),
			Service("default", "service-b", WithServiceType(corev1.ServiceTypeNodePort),
				WithPort("1337", 1337, 5003, corev1.ProtocolTCP),
			),
		}, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(errs).To(BeEmpty())

		createPayload := tree.ToCreatePayload(nil, "", "")

		Expect(createPayload.Listeners[0].Http.Hosts[0].Host).To(HaveValue(Equal(host)))

		// The following assertions require that target pool are sorted by the ingress UID and path.
		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[0].Path.ExactMatch).To(HaveValue(Equal("/5000")))
		Expect(createPayload.TargetPools[0].Name).To(Equal(createPayload.Listeners[0].Http.Hosts[0].Rules[0].TargetPool))
		Expect(createPayload.TargetPools[0].TargetPort).To(HaveValue(Equal(int32(5000))))

		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[1].Path.ExactMatch).To(HaveValue(Equal("/5001")))
		Expect(createPayload.TargetPools[1].Name).To(Equal(createPayload.Listeners[0].Http.Hosts[0].Rules[1].TargetPool))
		Expect(createPayload.TargetPools[1].TargetPort).To(HaveValue(Equal(int32(5001))))

		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[2].Path.ExactMatch).To(HaveValue(Equal("/5002")))
		Expect(createPayload.TargetPools[2].Name).To(Equal(createPayload.Listeners[0].Http.Hosts[0].Rules[2].TargetPool))
		Expect(createPayload.TargetPools[2].TargetPort).To(HaveValue(Equal(int32(5002))))

		Expect(createPayload.Listeners[0].Http.Hosts[0].Rules[3].Path.ExactMatch).To(HaveValue(Equal("/5003")))
		Expect(createPayload.TargetPools[3].Name).To(Equal(createPayload.Listeners[0].Http.Hosts[0].Rules[3].TargetPool))
		Expect(createPayload.TargetPools[3].TargetPort).To(HaveValue(Equal(int32(5003))))
	})

	It("should not expose ingress on HTTP if configured HTTPS-only", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress",
					WithAnnotation(AnnotationHTTPSOnly, "true"), WithTLSSecret("my-cert"),
					WithRule("my-host.local", WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80})),
				),
			},
			[]corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "my-cert"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
					},
				},
			}, []corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		Expect(tree.listeners).To(And(HaveLen(1), HaveKey(BeEquivalentTo(443))))
	})

	It("should return an error when the TLS secret doesn't exist", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-with-tls-secret-reference", WithTLSSecret("doesnt-exist"), WithRule("my-host.local", WithPath(
					"/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80},
				))),
				Ingress(corev1.NamespaceDefault, "ingress-http-only", WithRule("my-host.local", WithPath(
					"/.well-known/acme-challenge", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80},
				))),
			},
			nil, []corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-with-tls-secret-reference"),
				"Description": Equal("TLS secret doesn't exist"),
				"FieldPath":   Equal(field.NewPath("spec", "tls").Index(0).Child("secretName")),
			}),
		))
		create := tree.ToCreatePayload(nil, "network-id", "eu01")
		// HTTP rules should still work.
		Expect(create.Listeners).To(HaveLen(1))
		Expect(create.Listeners[0].Port).To(HaveValue(BeEquivalentTo(80)))
		Expect(create.Listeners[0].Http.Hosts).To(HaveLen(1))
		Expect(create.Listeners[0].Http.Hosts[0].Rules).To(HaveLen(2))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[0].Path.Prefix).To(HaveValue(Equal("/.well-known/acme-challenge")))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[1].Path.Prefix).To(HaveValue(Equal("/")))
	})

	It("should return an error when the TLS secret isn't of type TLS", func() {
		_, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-with-tls-secret-reference", WithTLSSecret("non-tls")),
			},
			[]corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "non-tls"},
					Type:       corev1.SecretTypeDockerConfigJson, // Not TLS
				},
			}, nil, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-with-tls-secret-reference"),
				"Description": Equal("TLS secret isn't of type kubernetes.io/tls"),
				"FieldPath":   Equal(field.NewPath("spec", "tls").Index(0).Child("secretName")),
			}),
		))
	})

	It("should return an error when TLS secret parsing fails", func() {
		_, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-with-tls-secret-reference", WithTLSSecret("invalid-tls")),
			},
			[]corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "invalid-tls"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte("invalid cert"),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
					},
				},
			}, nil, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-with-tls-secret-reference"),
				"Description": Equal("invalid certificate: tls: failed to find any PEM data in certificate input"),
				"FieldPath":   Equal(field.NewPath("spec", "tls").Index(0).Child("secretName")),
			}),
		))
	})

	It("should process TLS secret correctly and return it as missing certificate", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-with-tls-secret-reference", WithTLSSecret("my-tls")),
			},
			[]corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "my-tls"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
					},
				},
			}, nil, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		Expect(tree.GetMissingCertificates(nil)).To(ConsistOf(
			WorkTreeCertificate{
				PublicKey:  testdata.FixtureTLS1PublicKey,
				PrivateKey: testdata.FixtureTLS1PrivateKey,
				Ports:      map[uint16]any{443: nil},
			},
		))
	})

	It("should return unused certificates that are no longer used by the ALB", func() {
		tree, errs, err := BuildTree(&networkingv1.IngressClass{}, []networkingv1.Ingress{
			Ingress(corev1.NamespaceDefault, "ingress-with-tls-secret-reference", WithTLSSecret("my-tls")),
		}, []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "my-tls"},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
					corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
				},
			},
		}, []corev1.Service{}, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		Expect(tree.GetUnusedCertificates(map[CertificateFingerprint]string{
			testdata.FixtureTLS1FingerprintSHA256: "id-1",
			testdata.FixtureTLS2FingerprintSHA256: "id-2",
			testdata.FixtureTLS3FingerprintSHA256: "id-3",
		})).To(Equal(map[CertificateFingerprint]string{
			testdata.FixtureTLS2FingerprintSHA256: "id-2",
			testdata.FixtureTLS3FingerprintSHA256: "id-3",
		}))
	})

	It("should use TLS certificates only on ports that reference it", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationHTTPSOnly: "true"}},
			},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-a", WithTLSSecret("shared-cert"), WithTLSSecret("cert-for-a"),
					WithRule("host-a.local", WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80})),
				),
				Ingress(corev1.NamespaceDefault, "ingress-b", WithTLSSecret("shared-cert"), WithTLSSecret("cert-for-b"), WithAnnotation(AnnotationHTTPSPort, "444"),
					WithRule("host-b.local", WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80})),
				),
			},
			[]corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "cert-for-a"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "cert-for-b"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte(testdata.FixtureTLS2PublicKey),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS2PrivateKey),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "shared-cert"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte(testdata.FixtureTLS3PublicKey),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS3PrivateKey),
					},
				},
			}, []corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(map[CertificateFingerprint]string{
			testdata.FixtureTLS1FingerprintSHA256: "id-cert-1",
			testdata.FixtureTLS2FingerprintSHA256: "id-cert-2",
			testdata.FixtureTLS3FingerprintSHA256: "id-cert-3",
		}, "my-network", "region")
		Expect(create.Listeners).To(HaveLen(2))
		Expect(create.Listeners[0].Port).To(HaveValue(BeEquivalentTo(443)))
		Expect(create.Listeners[0].Https.CertificateConfig.CertificateIds).To(ConsistOf(
			"id-cert-1",
			"id-cert-3",
		))
		Expect(create.Listeners[1].Port).To(HaveValue(BeEquivalentTo(444)))
		Expect(create.Listeners[1].Https.CertificateConfig.CertificateIds).To(ConsistOf(
			"id-cert-2",
			"id-cert-3",
		))
	})

	It("should enable websocket if enable on ingress class", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationWebSocket: "true",
					},
				},
			},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/a", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
				Ingress(corev1.NamespaceDefault, "ingress-1", WithAnnotation(AnnotationWebSocket, "false"), WithRule("my-host.local",
					WithPath("/b", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.Listeners).To(HaveLen(1))
		Expect(create.Listeners[0].Http.Hosts).To(HaveLen(1))
		Expect(create.Listeners[0].Http.Hosts[0].Rules).To(HaveLen(2))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[0].Path.Prefix).To(HaveValue(Equal("/a")))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[0].WebSocket).To(HaveValue(BeTrue()))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[1].Path.Prefix).To(HaveValue(Equal("/b")))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[1].WebSocket).To(Or(BeNil(), HaveValue(BeFalse())))
	})

	It("should enable websocket if enable on ingress", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/a", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
				Ingress(corev1.NamespaceDefault, "ingress-1", WithAnnotation(AnnotationWebSocket, "true"), WithRule("my-host.local",
					WithPath("/b", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.Listeners).To(HaveLen(1))
		Expect(create.Listeners[0].Http.Hosts).To(HaveLen(1))
		Expect(create.Listeners[0].Http.Hosts[0].Rules).To(HaveLen(2))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[0].Path.Prefix).To(HaveValue(Equal("/a")))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[0].WebSocket).To(HaveValue(Or(BeNil(), HaveValue(BeFalse()))))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[1].Path.Prefix).To(HaveValue(Equal("/b")))
		Expect(create.Listeners[0].Http.Hosts[0].Rules[1].WebSocket).To(HaveValue(BeTrue()))
	})

	It("should set WAF on all ports if specified on ingress class", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationWAFName: "my-waf",
					},
				},
			},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
				Ingress(corev1.NamespaceDefault, "ingress-1", WithAnnotation(AnnotationHTTPPort, "8080"), WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.Listeners).To(HaveLen(2))
		Expect(create.Listeners[0].WafConfigName).To(HaveValue(Equal("my-waf")))
		Expect(create.Listeners[1].WafConfigName).To(HaveValue(Equal("my-waf")))
	})

	It("should set allowed source range on all ports if specified on ingress class", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationAllowedSourceRanges: "10.0.0.0/24,1.2.3.4/32",
					},
				},
			}, nil, nil, nil, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.Options.AccessControl.AllowedSourceRanges).To(HaveExactElements("10.0.0.0/24", "1.2.3.4/32"))
	})

	It("should set ALB to internal if annotation is true", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationInternal: "true",
					},
				},
			}, nil, nil, nil, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.Options.PrivateNetworkOnly).To(HaveValue(BeTrue()))
		Expect(create.Options.EphemeralAddress).To(HaveValue(BeFalse()))
	})

	It("should set ALB to static if annotation contains IP", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationExternalIP: "1.2.3.4",
					},
				},
			}, nil, nil, nil, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.ExternalAddress).To(HaveValue(Equal("1.2.3.4")))
		Expect(create.Options.EphemeralAddress).To(HaveValue(BeFalse()))
	})

	It("should return errors for paths that exceed the target pool limit ", func() {
		ingresses := []networkingv1.Ingress{}
		// We create a matrix of resources based on all sorting criteria.
		for prio := range 2 { // 2 * 2 * 2 * 3 paths = 24
			for age := range 2 { // Higher age means older
				for alphabet := range 2 {
					ingresses = append(ingresses, Ingress(corev1.NamespaceDefault,
						fmt.Sprintf("ingress-prio-%d-age-%d-%s", prio, age, string(rune('a'+alphabet))),
						func(ingress *networkingv1.Ingress) {
							ingress.CreationTimestamp = metav1.NewTime(time.Unix(100000, 0).Add(-time.Duration(age) * time.Hour))
						},
						WithAnnotation(AnnotationPriority, fmt.Sprintf("%d", prio)),
						WithRule("my-host.local",
							WithPath(fmt.Sprintf("/prio-%d-age-%d-%d-0", prio, age, alphabet), new(networkingv1.PathTypeExact),
								"my-service", networkingv1.ServiceBackendPort{Number: 80}),
							WithPath(fmt.Sprintf("/prio-%d-age-%d-%d-1", prio, age, alphabet), new(networkingv1.PathTypeExact),
								"my-service", networkingv1.ServiceBackendPort{Number: 80}),
							WithPath(fmt.Sprintf("/prio-%d-age-%d-%d-2", prio, age, alphabet), new(networkingv1.PathTypeExact),
								"my-service", networkingv1.ServiceBackendPort{Number: 80}),
						)))
				}
			}
		}
		_, errs, err := BuildTree(
			&networkingv1.IngressClass{}, ingresses, nil, []corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-prio-0-age-0-b"),
				"Description": Equal("Target pool limit reached. Path will be ignored."),
				"FieldPath":   Equal(field.NewPath("spec", "rules").Index(0).Child("paths").Index(0)),
			}),
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-prio-0-age-0-b"),
				"Description": Equal("Target pool limit reached. Path will be ignored."),
				"FieldPath":   Equal(field.NewPath("spec", "rules").Index(0).Child("paths").Index(1)),
			}),
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-prio-0-age-0-b"),
				"Description": Equal("Target pool limit reached. Path will be ignored."),
				"FieldPath":   Equal(field.NewPath("spec", "rules").Index(0).Child("paths").Index(2)),
			}),
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-prio-0-age-0-a"),
				"Description": Equal("Target pool limit reached. Path will be ignored."),
				"FieldPath":   Equal(field.NewPath("spec", "rules").Index(0).Child("paths").Index(2)),
			}),
		))
	})

	It("should set target pool TLS settings", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationTargetPoolTLSEnabled: "true",
					},
				},
			},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithUID("uid-1"), WithRule("my-host.local",
					WithPath("/inherit", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
					WithPath("/overwrite-disable-on-service", new(networkingv1.PathTypePrefix), "service-with-tls-disabled", networkingv1.ServiceBackendPort{Number: 80}),
				)),
				Ingress(corev1.NamespaceDefault, "ingress-2", WithUID("uid-2"), WithAnnotation(AnnotationTargetPoolTLSEnabled, "false"),
					WithRule("my-host.local",
						WithPath("/overwrite-disable-on-ingress", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
					)),
				Ingress(corev1.NamespaceDefault, "ingress-3", WithUID("uid-3"), WithAnnotation(AnnotationTargetPoolTLSCustomCa, "custom-ca"),
					WithRule("my-host.local",
						WithPath("/custom-ca", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
					)),
				Ingress(corev1.NamespaceDefault, "ingress-4", WithUID("uid-4"), WithAnnotation(AnnotationTargetPoolTLSSkipCertificateValidation, "true"),
					WithRule("my-host.local",
						WithPath("/skip-validation", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
					)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
				Service(corev1.NamespaceDefault, "service-with-tls-disabled", WithServiceAnnotation(AnnotationTargetPoolTLSEnabled, "false"),
					WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30001, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.TargetPools).To(HaveLen(5))
		Expect(create.TargetPools[0].TlsConfig.Enabled).To(HaveValue(BeTrue()))
		Expect(create.TargetPools[1].TlsConfig.Enabled).To(HaveValue(BeFalse()))
		Expect(create.TargetPools[2].TlsConfig.Enabled).To(HaveValue(BeFalse()))
		Expect(create.TargetPools[3].TlsConfig.CustomCa).To(HaveValue(Equal("custom-ca")))
		Expect(create.TargetPools[4].TlsConfig.SkipCertificateValidation).To(HaveValue(BeTrue()))
	})

	It("should use the log configuration from the existing load balancer", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{}, nil, nil, nil, nil, &v2api.LoadBalancer{
				Options: &v2api.LoadBalancerOptions{
					Observability: &v2api.LoadbalancerOptionObservability{
						Logs: &v2api.LoadbalancerOptionLogs{
							CredentialsRef: new("my-creds"),
							PushUrl:        new("my-push-url"),
						},
					},
				},
			},
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		update := tree.ToUpdatePayload(nil, "network-id", "region")
		Expect(update.Options.Observability.Logs.CredentialsRef).To(HaveValue(Equal("my-creds")))
		Expect(update.Options.Observability.Logs.PushUrl).To(HaveValue(Equal("my-push-url")))
	})

	It("should use the version from the existing load balancer in update payload", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{}, nil, nil, nil, nil, &v2api.LoadBalancer{
				Version: new("current-version"),
			},
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		update := tree.ToUpdatePayload(nil, "network-id", "region")
		Expect(update.Version).To(HaveValue(Equal("current-version")))
	})

	It("should turn implementation-specific paths into exact matchers", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/a", new(networkingv1.PathTypeImplementationSpecific), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.Listeners[0].Http.Hosts[0].Rules[0].Path.ExactMatch).To(HaveValue(Equal("/a")))
	})

	It("should return an error on duplicate paths", func() {
		_, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
				Ingress(corev1.NamespaceDefault, "ingress-2", WithAnnotation(AnnotationPriority, "10"), WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-1"),
				"Description": Equal("Path already exists"),
				"FieldPath":   Equal(field.NewPath("spec", "rules").Index(0).Child("paths").Index(0)),
			}),
		))
	})

	It("should drop target pools with etp=Local and missing health check port", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service",
					WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP),
					func(service *corev1.Service) {
						service.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyLocal
					},
				),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-1"),
				"Description": Equal("Service has externalTrafficPolicy=Local but doesn't have a health check node port. The service must be of type LoadBalancer."),
				"FieldPath":   Equal(field.NewPath("spec", "rules").Index(0).Child("paths").Index(0).Child("backend", "service")),
			}),
		))
		Expect(tree.targetPools).To(BeEmpty())
	})

	It("should filter out nodes that don't meet the criteria to serve traffic", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-that-meets-all-criteria",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "10.0.0.1",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-with-termination-condition",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{
								Type:   ConditionNodeTermination,
								Status: corev1.ConditionTrue,
							},
						},
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "10.0.0.2",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-with-tobedeleted-taint",
					},
					Spec: corev1.NodeSpec{
						Taints: []corev1.Taint{
							{
								Key: TaintToBeDeleted,
							},
						},
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "10.0.0.3",
							},
						},
					},
				},
			}, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		Expect(create.TargetPools).To(HaveLen(1))
		Expect(create.TargetPools[0].Targets).To(ConsistOf(
			v2api.Target{
				DisplayName: new("node-that-meets-all-criteria"),
				Ip:          new("10.0.0.1"),
			},
		))
	})

	It("should have all slices ordered consistently", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithUID("ingress-1-uid"), WithRule("b.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "service-b", networkingv1.ServiceBackendPort{Number: 80}),
				)),
				Ingress(corev1.NamespaceDefault, "ingress-2", WithUID("ingress-2-uid"), WithAnnotation(AnnotationHTTPPort, "8080"),
					WithRule("c.local", WithPath("/", new(networkingv1.PathTypePrefix), "service-c", networkingv1.ServiceBackendPort{Number: 80})),
					WithRule("a.local", WithPath("/", new(networkingv1.PathTypePrefix), "service-a", networkingv1.ServiceBackendPort{Number: 80})),
				),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "service-c", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30002, corev1.ProtocolTCP)),
				Service(corev1.NamespaceDefault, "service-b", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30001, corev1.ProtocolTCP)),
				Service(corev1.NamespaceDefault, "service-a", WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP)),
			}, []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-b",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "10.0.0.2",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-a",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "10.0.0.1",
							},
						},
					},
				},
			}, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(BeEmpty())
		create := tree.ToCreatePayload(nil, "network-id", "region")
		// Sorting of path is done in a separate test.
		Expect(create.Listeners).To(HaveExactElements(
			MatchFields(IgnoreExtras, Fields{
				"Port": HaveValue(BeEquivalentTo(80)),
				"Http": HaveValue(MatchFields(IgnoreExtras, Fields{
					"Hosts": HaveExactElements(
						MatchFields(IgnoreExtras, Fields{
							"Host": HaveValue(Equal("b.local")),
						}),
					),
				})),
			}),
			MatchFields(IgnoreExtras, Fields{
				"Port": HaveValue(BeEquivalentTo(8080)),
				"Http": HaveValue(MatchFields(IgnoreExtras, Fields{
					"Hosts": HaveExactElements(
						MatchFields(IgnoreExtras, Fields{
							"Host": HaveValue(Equal("a.local")),
						}),
						MatchFields(IgnoreExtras, Fields{
							"Host": HaveValue(Equal("c.local")),
						}),
					),
				})),
			}),
		))
		haveTargets := func() types.GomegaMatcher {
			return HaveExactElements(
				MatchFields(IgnoreExtras, Fields{
					"Ip": HaveValue(Equal("10.0.0.1")),
				}),
				MatchFields(IgnoreExtras, Fields{
					"Ip": HaveValue(Equal("10.0.0.2")),
				}),
			)
		}
		Expect(create.TargetPools).To(HaveExactElements(
			MatchFields(IgnoreExtras, Fields{
				"Name":    HaveValue(Equal("ingress-1-uid-0-0")),
				"Targets": haveTargets(),
			}),
			MatchFields(IgnoreExtras, Fields{
				"Name":    HaveValue(Equal("ingress-2-uid-0-0")),
				"Targets": haveTargets(),
			}),
			MatchFields(IgnoreExtras, Fields{
				"Name":    HaveValue(Equal("ingress-2-uid-1-0")),
				"Targets": haveTargets(),
			}),
		))
	})

	It("should error if HTTPS port is out of range", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationHTTPSPort: "-3",
					},
				},
			},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithTLSSecret("my-cert"), WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			[]corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "my-cert"},
					Type:       corev1.SecretTypeTLS,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
						corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
					},
				},
			},
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service",
					WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP),
				),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-1"),
				"Description": Equal("HTTPS port is out of range."),
				"FieldPath":   BeNil(),
			}),
		))
		Expect(tree.targetPools).To(BeEmpty())
	})

	It("should error if HTTP port is out of range", func() {
		tree, errs, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationHTTPPort: "70000",
					},
				},
			},
			[]networkingv1.Ingress{
				Ingress(corev1.NamespaceDefault, "ingress-1", WithRule("my-host.local",
					WithPath("/", new(networkingv1.PathTypePrefix), "my-service", networkingv1.ServiceBackendPort{Number: 80}),
				)),
			},
			nil,
			[]corev1.Service{
				Service(corev1.NamespaceDefault, "my-service",
					WithServiceType(corev1.ServiceTypeNodePort), WithPort("my-port", 80, 30000, corev1.ProtocolTCP),
				),
			}, nil, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(errs).To(ConsistOf(
			MatchAllFields(Fields{
				"Ingress":     testutil.HaveName("ingress-1"),
				"Description": Equal("HTTP port is out of range."),
				"FieldPath":   BeNil(),
			}),
		))
		Expect(tree.targetPools).To(BeEmpty())
	})

	DescribeTable("external IP", func(externalIP, expectErr string) {
		_, _, err := BuildTree(
			&networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationExternalIP: externalIP,
					},
				},
			}, nil, nil, nil, nil, nil,
		)
		if expectErr != "" {
			Expect(err).To(MatchError(expectErr))
		}
	},
		Entry("valid", "10.0.0.1", ""),
		Entry("UUID not supported", "00000000-0000-0000-0000-000000000000", `failed to parse external IP annotation: ParseAddr("00000000-0000-0000-0000-000000000000"): unable to parse IP`),
		Entry("CIDR not supported", "10.0.0.1/24", `failed to parse external IP annotation: ParseAddr("10.0.0.1/24"): unexpected character (at "/24")`),
		Entry("IPv6 not supported", "2001:db8::1", "external IP annotation is not an IPv4 address"),
	)
})
