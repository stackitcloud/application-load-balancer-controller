package ingress_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec/testdata"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/stackit"
	stackitconfig "github.com/stackitcloud/application-load-balancer-controller/pkg/stackit/config"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/testutil"
	. "github.com/stackitcloud/application-load-balancer-controller/pkg/testutil/ingress"
	. "github.com/stackitcloud/application-load-balancer-controller/pkg/testutil/service"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	certsdk "github.com/stackitcloud/stackit-sdk-go/services/certificates/v2api"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	projectID      = "dummy-project-id"
	region         = "eu01"
	networkID      = "my-network"
	controllerName = "stackit.cloud/alb-ingress"
	finalizerName  = "stackit.cloud/alb-ingress"
	targetCertID   = "real-certificate-uuid-abc-123"
)

var _ = Describe("IngressClassController", func() {
	var (
		recorder *record.FakeRecorder

		// namespace is the namespace in which all namespaced resources of the test case should go.
		// It is cleaned up automatically when the test ends and all resource deletions will be finalized before the test case completes.
		namespace *corev1.Namespace

		mockCtrl   *gomock.Controller
		albClient  *stackit.MockApplicationLoadBalancerClient
		certClient *stackit.MockCertificatesClient

		node corev1.Node

		mgrContext        context.Context
		mgrCancel         context.CancelFunc
		managerTerminated sync.WaitGroup
	)

	BeforeEach(func(ctx context.Context) {

		mockCtrl = gomock.NewController(GinkgoT())
		recorder = record.NewFakeRecorder(10)

		albClient = stackit.NewMockApplicationLoadBalancerClient(mockCtrl)
		certClient = stackit.NewMockCertificatesClient(mockCtrl)
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

		reconciler := ingress.IngressClassReconciler{
			Recorder:          recorder,
			Client:            mgr.GetClient(),
			Scheme:            mgr.GetScheme(),
			ALBClient:         albClient,
			CertificateClient: certClient,
			ALBConfig: stackitconfig.ALBConfig{
				Global: stackitconfig.GlobalOpts{
					ProjectID: projectID,
					Region:    region,
				},
				ApplicationLoadBalancer: stackitconfig.ApplicationLoadBalancerOpts{NetworkID: networkID}},
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
			mockCtrl.Finish()
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

		})
	})

	It("should create an empty ALB for an ingress class matching the controller", func(ctx context.Context) {
		getLoadBalancerResponse := &atomic.Pointer[albsdk.LoadBalancer]{}
		certClient.EXPECT().ListCertificate(gomock.Any(), gomock.Any(), gomock.Any()).Return(new(certsdk.ListCertificatesResponse{
			Items: []certsdk.GetCertificateResponse{},
		}), nil).AnyTimes()
		albClient.EXPECT().GetLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, _, _ string) (*albsdk.LoadBalancer, error) {
			lb := getLoadBalancerResponse.Load()
			if lb == nil {
				return nil, stackit.ErrorNotFound
			}
			return lb, nil
		}).AnyTimes()
		albClient.EXPECT().CreateLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, _ string, create *albsdk.CreateLoadBalancerPayload) (*albsdk.LoadBalancer, error) {
			response := albsdk.LoadBalancer(*create)
			response.Version = new("version-after-create")
			response.ExternalAddress = new("127.0.0.1")
			response.Status = new(stackit.LBStatusReady)
			getLoadBalancerResponse.Store(&response)
			return &response, nil
		}).Times(1)

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
			albClient.EXPECT().DeleteLoadBalancer(gomock.Any(), projectID, region, spec.LoadBalancerName(ingressClass)).Times(1)
			testutil.DeleteAndWaitForKubernetesResource(ctx, k8sClient, ingressClass)
		})

		WaitUntilFinalizerAttached(ctx, k8sClient, ingressClass)

		Eventually(getLoadBalancerResponse).Should(testutil.HaveAtomicValue[albsdk.LoadBalancer](Not(BeNil())))
	})

	// The ALB is already created when BeforeEach completes.
	Context("with IngressClass matching the controller", func() {
		var (
			ingressClass *networkingv1.IngressClass

			getLoadBalancerResponse  *atomic.Pointer[albsdk.LoadBalancer]
			listCertificatesResponse *atomic.Pointer[certsdk.ListCertificatesResponse]
		)

		BeforeEach(func(ctx context.Context) {
			getLoadBalancerResponse = &atomic.Pointer[albsdk.LoadBalancer]{}
			listCertificatesResponse = &atomic.Pointer[certsdk.ListCertificatesResponse]{}
			listCertificatesResponse.Store(&certsdk.ListCertificatesResponse{Items: []certsdk.GetCertificateResponse{}})

			certClient.EXPECT().ListCertificate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, _ string) (*certsdk.ListCertificatesResponse, error) {
				return listCertificatesResponse.Load(), nil
			}).AnyTimes()

			albClient.EXPECT().GetLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, _, _ string) (*albsdk.LoadBalancer, error) {
				lb := getLoadBalancerResponse.Load()
				if lb == nil {
					return nil, stackit.ErrorNotFound
				}
				return lb, nil
			}).AnyTimes()
			albClient.EXPECT().CreateLoadBalancer(gomock.Any(), projectID, region, gomock.Any()).DoAndReturn(func(_ context.Context, _, _ string, create *albsdk.CreateLoadBalancerPayload) (*albsdk.LoadBalancer, error) {
				response := albsdk.LoadBalancer(*create)
				response.Version = new("version-after-create")
				response.ExternalAddress = new("127.0.0.1")
				response.Status = new(stackit.LBStatusReady)
				getLoadBalancerResponse.Store(&response)
				return &response, nil
			}).Times(1)

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
				albClient.EXPECT().DeleteLoadBalancer(gomock.Any(), projectID, region, spec.LoadBalancerName(ingressClass)).MinTimes(1)
				testutil.DeleteAndWaitForKubernetesResource(ctx, k8sClient, ingressClass)
			})

			// Wait for CreateLoadBalancer to be called, i.e. getLoadBalancerResponse to not be nil.
			Eventually(getLoadBalancerResponse).Should(testutil.HaveAtomicValue[albsdk.LoadBalancer](Not(BeNil())))
		})

		It("should create certificate and reference it in ALB", func(ctx context.Context) {
			updateRequest := &atomic.Pointer[albsdk.UpdateLoadBalancerPayload]{}
			certClient.EXPECT().CreateCertificate(gomock.Any(), projectID, region, gomock.Any()).DoAndReturn(func(_ context.Context, _, _ string, certificate *certsdk.CreateCertificatePayload) (*certsdk.GetCertificateResponse, error) {
				fingerprint, err := spec.ValidateTLSCertAndFingerprint([]byte(*certificate.PublicKey), []byte(*certificate.PrivateKey))
				if err != nil {
					return nil, fmt.Errorf("invalid certificate: %w", err)
				}
				response := certsdk.GetCertificateResponse{
					Name:   certificate.Name,
					Id:     new("random-certificate-id"),
					Labels: certificate.Labels,
					Data: &certsdk.Data{
						FingerprintSha256: new(fingerprint),
					},
					PublicKey: certificate.PublicKey,
				}
				listCertificatesResponse.Store(&certsdk.ListCertificatesResponse{
					Items: []certsdk.GetCertificateResponse{response},
				})
				return &response, nil
			}).Times(1)
			certClient.EXPECT().DeleteCertificate(gomock.Any(), projectID, region, "random-certificate-id").Return(nil).AnyTimes()
			albClient.EXPECT().UpdateLoadBalancer(gomock.Any(), projectID, region, gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, _, _ string, update *albsdk.UpdateLoadBalancerPayload) (*albsdk.LoadBalancer, error) {
				response := albsdk.LoadBalancer(*update)
				response.Version = new("version-after-update")
				response.ExternalAddress = new("127.0.0.1")
				response.Status = new(stackit.LBStatusReady)
				getLoadBalancerResponse.Store(&response)

				updateRequest.Store(update)
				return (*albsdk.LoadBalancer)(update), nil
			}).MinTimes(1)

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

			// Depending on in which order the secret and service hit the cache the first update might not yet include the certificate.
			Eventually(updateRequest).Should(testutil.HaveAtomicValue[albsdk.UpdateLoadBalancerPayload](
				WithTransform(func(u *albsdk.UpdateLoadBalancerPayload) ([]string, error) {
					if u == nil {
						return nil, errors.New("no update happened")
					}
					if len(u.Listeners) != 2 {
						return nil, errors.New("expect two listeners")
					}
					httpsListener := u.Listeners[1]
					if httpsListener.Https == nil || httpsListener.Https.CertificateConfig == nil {
						return nil, errors.New("certificates config is nil")
					}
					return httpsListener.Https.CertificateConfig.CertificateIds, nil
				}, ConsistOf("random-certificate-id")),
			))
		})

		// TODO: Test changes to nodes
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
