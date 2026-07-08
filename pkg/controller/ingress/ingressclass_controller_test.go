package ingress

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec/testdata"
	stackitconfig "github.com/stackitcloud/application-load-balancer-controller/pkg/stackit/config"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/stackit/fake"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/testutil"
	. "github.com/stackitcloud/application-load-balancer-controller/pkg/testutil/ingress"
	. "github.com/stackitcloud/application-load-balancer-controller/pkg/testutil/service"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	"github.com/stackitcloud/stackit-sdk-go/services/certificates/v2api"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	projectID = "dummy-project-id"
	region    = "eu01"
	networkID = "my-network"
)

var _ = Describe("IngressClassController", func() {
	var (
		recorder *events.FakeRecorder

		// namespace is the namespace in which all namespaced resources of the test case should go.
		// It is cleaned up automatically when the test ends and all resource deletions will be finalized before the test case completes.
		namespace *corev1.Namespace

		albFake  *fake.ALB
		certFake *fake.Certs

		node corev1.Node

		mgrContext        context.Context
		mgrCancel         context.CancelFunc
		managerTerminated sync.WaitGroup
	)

	BeforeEach(func(ctx context.Context) {
		recorder = events.NewFakeRecorder(10)

		albFake = fake.NewALB()
		certFake = fake.NewCerts()
		// Make the fake's fingerprint match what the controller computes locally
		// so that existing certificate lookup by fingerprint works.
		certFake.Fingerprint = spec.ValidateTLSCertAndFingerprint

		mgrContext, mgrCancel = context.WithCancel(context.Background())

		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "stackit-alb-ingress-test-",
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func(ctx context.Context) {
			// There is no namespace controller deployed. So the content of the namespace won't be cleaned up by Kubernetes itself.
			Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
		})

		node = corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.10.10.10"}},
			},
		}
		testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &node)

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		reconciler := IngressClassReconciler{
			Recorder:          recorder,
			Client:            mgr.GetClient(),
			ALBClient:         albFake,
			CertificateClient: certFake,
			ALBConfig: stackitconfig.ALBConfig{
				Global: stackitconfig.GlobalOpts{
					ProjectID: projectID,
					Region:    region,
				},
				ApplicationLoadBalancer: stackitconfig.ApplicationLoadBalancerOpts{NetworkID: networkID},
			},
		}

		Expect(reconciler.SetupWithManager(ctx, mgr, namespace.Name)).To(Succeed())

		managerTerminated.Add(1)
		go func() {
			defer GinkgoRecover()
			err = mgr.Start(mgrContext)
			managerTerminated.Done()
			Expect(err).NotTo(HaveOccurred())
		}()
		DeferCleanup(func() {
			mgrCancel()
			// Canceling the context doesn't cause the manager to stop immediately.
			// We have to wait for manager.Start() to return to ensure that the manager doesn't "spill" into the next test case.
			managerTerminated.Wait()
		})
	})

	Context("when the IngressClass does not match controller", func() {
		It("should ignore the IngressClass and not append finalizers", func(ctx context.Context) {
			ignoredIngressClass := &networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "ignored-ingressclass-",
				},
				Spec: networkingv1.IngressClassSpec{
					Controller: "some.other/controller",
				},
			}
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, ignoredIngressClass)

			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ignoredIngressClass), ignoredIngressClass)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ignoredIngressClass.Finalizers).To(BeEmpty())
			}, "2s", "200ms").Should(Succeed())

			Expect(albFake.Calls()).To(BeEmpty(), "controller must not touch the ALB API for unrelated IngressClasses")
			Expect(certFake.Calls()).To(BeEmpty(), "controller must not touch the certificates API for unrelated IngressClasses")
		})
	})

	It("should create an empty ALB for an ingress class matching the controller", func(ctx context.Context) {
		ingressClass := &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "managed-ingressclass-",
			},
			Spec: networkingv1.IngressClassSpec{
				Controller: controllerName,
			},
		}
		Expect(k8sClient.Create(ctx, ingressClass)).To(Succeed())
		DeferCleanup(func(ctx context.Context) {
			testutil.DeleteAndWaitForKubernetesResource(ctx, k8sClient, ingressClass)
			Expect(albFake.CallsOf("DeleteLoadBalancer")).To(HaveLen(1))
		})

		WaitUntilFinalizerAttached(ctx, k8sClient, ingressClass)

		Eventually(func() *albsdk.LoadBalancer {
			return albFake.LoadBalancer(projectID, region, spec.LoadBalancerName(ingressClass))
		}).ShouldNot(BeNil())
	})

	// The ALB is already created when BeforeEach completes.
	Context("with IngressClass matching the controller and no annotations", func() {
		var ingressClass *networkingv1.IngressClass

		BeforeEach(func(ctx context.Context) {
			ingressClass = &networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "ingressclass-",
				},
				Spec: networkingv1.IngressClassSpec{
					Controller: controllerName,
				},
			}
			Expect(k8sClient.Create(ctx, ingressClass)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				testutil.DeleteAndWaitForKubernetesResource(ctx, k8sClient, ingressClass)
			})

			// Wait until the load balancer is created.
			Eventually(func() *albsdk.LoadBalancer {
				return albFake.LoadBalancer(projectID, region, spec.LoadBalancerName(ingressClass))
			}).ShouldNot(BeNil())
		})

		It("should create certificate and reference it in ALB", func(ctx context.Context) {
			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "my-tls-cert"},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
					corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
				},
			}
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &secret)
			service := Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("http", 80, 30000, corev1.ProtocolTCP))
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &service)
			ingress := Ingress(corev1.NamespaceDefault, "my-ingress", WithIngressClass(ingressClass.Name), WithTLSSecret(secret.Name),
				WithRule("my-host.local", WithPath("/", new(networkingv1.PathTypePrefix), service.Name, networkingv1.ServiceBackendPort{Number: 80})),
			)
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &ingress)

			// Depending on the order in which secret and service hit the cache,
			// the first update might not yet include the certificate.
			Eventually(func(g Gomega) {
				lb := albFake.LoadBalancer(projectID, region, spec.LoadBalancerName(ingressClass))
				g.Expect(lb).NotTo(BeNil())
				g.Expect(lb.Listeners).To(HaveLen(2))
				httpsListener := lb.Listeners[1]
				g.Expect(httpsListener.Https).NotTo(BeNil())
				g.Expect(httpsListener.Https.CertificateConfig).NotTo(BeNil())
				g.Expect(httpsListener.Https.CertificateConfig.CertificateIds).To(HaveLen(1))
			}).Should(Succeed())

			certs := certFake.Certificates()
			Expect(certs).To(HaveLen(1))
			lb := albFake.LoadBalancer(projectID, region, spec.LoadBalancerName(ingressClass))
			Expect(lb.Listeners[1].Https.CertificateConfig.CertificateIds).To(ConsistOf(*certs[0].Id))
		})

		It("should delete duplicate certificates in the API", func(ctx context.Context) {
			_, err := certFake.CreateCertificate(ctx, projectID, region, &v2api.CreateCertificatePayload{
				Labels: &map[string]string{
					spec.LabelIngressClassUID: string(ingressClass.UID),
				},
				Name:       new("duplicate-cert-1"),
				PrivateKey: new(testdata.FixtureTLS1PrivateKey),
				PublicKey:  new(testdata.FixtureTLS1PublicKey),
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = certFake.CreateCertificate(ctx, projectID, region, &v2api.CreateCertificatePayload{
				Labels: &map[string]string{
					spec.LabelIngressClassUID: string(ingressClass.UID),
				},
				Name:       new("duplicate-cert-2"),
				PrivateKey: new(testdata.FixtureTLS1PrivateKey),
				PublicKey:  new(testdata.FixtureTLS1PublicKey),
			})
			Expect(err).NotTo(HaveOccurred())

			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: corev1.NamespaceDefault, Name: "my-tls-cert"},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte(testdata.FixtureTLS1PublicKey),
					corev1.TLSPrivateKeyKey: []byte(testdata.FixtureTLS1PrivateKey),
				},
			}
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &secret)
			service := Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("http", 80, 30000, corev1.ProtocolTCP))
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &service)
			ingress := Ingress(corev1.NamespaceDefault, "my-ingress", WithIngressClass(ingressClass.Name),
				WithAnnotation(spec.AnnotationHTTPSOnly, "true"), WithTLSSecret(secret.Name),
				WithRule("my-host.local", WithPath("/", new(networkingv1.PathTypePrefix), service.Name, networkingv1.ServiceBackendPort{Number: 80})),
			)
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &ingress)

			Eventually(func(g Gomega) {
				lb := albFake.LoadBalancer(projectID, region, spec.LoadBalancerName(ingressClass))
				g.Expect(lb).NotTo(BeNil())
				g.Expect(lb.Listeners).To(HaveLen(1))
				g.Expect(lb.Listeners[0].Https).NotTo(BeNil())
				g.Expect(lb.Listeners[0].Https.CertificateConfig.CertificateIds).To(HaveLen(1))

				g.Expect(certFake.Certificates()).To(ConsistOf(
					HaveValue(MatchFields(IgnoreExtras, Fields{
						"Id": HaveValue(Equal(lb.Listeners[0].Https.CertificateConfig.CertificateIds[0])),
					})),
				))
			}).Should(Succeed())
		})

		It("should set the public IP of the ALB in the status of each ingress for a public LB", func(ctx context.Context) {
			service := Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("http", 80, 30000, corev1.ProtocolTCP))
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &service)
			ingress1 := Ingress(corev1.NamespaceDefault, "ingress-1", WithIngressClass(ingressClass.Name),
				WithRule("host1.local", WithPath("/", new(networkingv1.PathTypePrefix), service.Name, networkingv1.ServiceBackendPort{Number: 80})),
			)
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &ingress1)
			ingress2 := Ingress(corev1.NamespaceDefault, "ingress-2", WithIngressClass(ingressClass.Name),
				WithRule("host2.local", WithPath("/", new(networkingv1.PathTypePrefix), service.Name, networkingv1.ServiceBackendPort{Number: 80})),
			)
			testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &ingress2)

			Eventually(ctx, testutil.KubernetesResource(k8sClient, &ingress1)).Should(HaveValue(MatchFields(IgnoreExtras, Fields{
				"Status": MatchFields(IgnoreExtras, Fields{
					"LoadBalancer": MatchFields(IgnoreExtras, Fields{
						"Ingress": HaveExactElements(MatchFields(IgnoreExtras, Fields{
							"IP": Equal(albFake.ExternalAddress),
						})),
					}),
				}),
			})))
			Eventually(ctx, testutil.KubernetesResource(k8sClient, &ingress2)).Should(HaveValue(MatchFields(IgnoreExtras, Fields{
				"Status": MatchFields(IgnoreExtras, Fields{
					"LoadBalancer": MatchFields(IgnoreExtras, Fields{
						"Ingress": HaveExactElements(MatchFields(IgnoreExtras, Fields{
							"IP": Equal(albFake.ExternalAddress),
						})),
					}),
				}),
			})))
		})

		// TODO: Test changes to nodes
	})

	It("should set the private IP of the ALB in the status of each ingress for a private LB", func(ctx context.Context) {
		ingressClass := &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "ingressclass-",
				Annotations: map[string]string{
					spec.AnnotationInternal: "true",
				},
			},
			Spec: networkingv1.IngressClassSpec{
				Controller: controllerName,
			},
		}
		testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, ingressClass)
		// Wait until the load balancer is created.
		Eventually(func() *albsdk.LoadBalancer {
			return albFake.LoadBalancer(projectID, region, spec.LoadBalancerName(ingressClass))
		}).ShouldNot(BeNil())

		service := Service(corev1.NamespaceDefault, "my-service", WithServiceType(corev1.ServiceTypeNodePort), WithPort("http", 80, 30000, corev1.ProtocolTCP))
		testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &service)
		ingress1 := Ingress(corev1.NamespaceDefault, "ingress-1", WithIngressClass(ingressClass.Name),
			WithRule("host1.local", WithPath("/", new(networkingv1.PathTypePrefix), service.Name, networkingv1.ServiceBackendPort{Number: 80})),
		)
		testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &ingress1)
		ingress2 := Ingress(corev1.NamespaceDefault, "ingress-2", WithIngressClass(ingressClass.Name),
			WithRule("host2.local", WithPath("/", new(networkingv1.PathTypePrefix), service.Name, networkingv1.ServiceBackendPort{Number: 80})),
		)
		testutil.CreateKubernetesResourceAndDeferDeletion(ctx, k8sClient, &ingress2)

		Eventually(ctx, testutil.KubernetesResource(k8sClient, &ingress1)).Should(HaveValue(MatchFields(IgnoreExtras, Fields{
			"Status": MatchFields(IgnoreExtras, Fields{
				"LoadBalancer": MatchFields(IgnoreExtras, Fields{
					"Ingress": HaveExactElements(MatchFields(IgnoreExtras, Fields{
						"IP": Equal(albFake.PrivateAddress),
					})),
				}),
			}),
		})))
		Eventually(ctx, testutil.KubernetesResource(k8sClient, &ingress2)).Should(HaveValue(MatchFields(IgnoreExtras, Fields{
			"Status": MatchFields(IgnoreExtras, Fields{
				"LoadBalancer": MatchFields(IgnoreExtras, Fields{
					"Ingress": HaveExactElements(MatchFields(IgnoreExtras, Fields{
						"IP": Equal(albFake.PrivateAddress),
					})),
				}),
			}),
		})))
	})
})

// WaitUntilFinalizerAttached blocks until the controller successfully injects our tracking string
func WaitUntilFinalizerAttached(ctx context.Context, cl client.Client, ic *networkingv1.IngressClass) {
	GinkgoHelper() // Tells Ginkgo to report failures on the line that calls this function, not here!

	reconciledIngressClass := &networkingv1.IngressClass{}
	Eventually(func(g Gomega) {
		err := cl.Get(ctx, client.ObjectKeyFromObject(ic), reconciledIngressClass)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(reconciledIngressClass.Finalizers).To(ContainElement(finalizerName))
	}, "5s", "200ms").Should(Succeed())
}
