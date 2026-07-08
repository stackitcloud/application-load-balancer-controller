package integration_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
	stackitconfig "github.com/stackitcloud/application-load-balancer-controller/pkg/stackit/config"
	sdkconfig "github.com/stackitcloud/stackit-sdk-go/core/config"
	"github.com/stackitcloud/stackit-sdk-go/core/oapierror"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	albwafsdk "github.com/stackitcloud/stackit-sdk-go/services/albwaf/v1alphaapi"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	controllerName          = "stackit.cloud/alb-ingress"
	annotationNetworkMode   = "alb.stackit.cloud/network-mode"
	annotationHTTPPort      = "alb.stackit.cloud/http-port"
	annotationHTTPSPort     = "alb.stackit.cloud/https-port"
	annotationHTTPSOnly     = "alb.stackit.cloud/https-only"
	annotationAllowedRanges = "alb.stackit.cloud/allowed-source-ranges"
	annotationWAFName       = "alb.stackit.cloud/web-application-firewall-name"
	networkModeNodePort     = "NodePort"
	initialHTTPPort         = 8080
	updatedHTTPPort         = 8090
	customHTTPSPort         = 8443
	albProvisionTimeout     = 15 * time.Minute
	albUpdateTimeout        = 10 * time.Minute
	resourceReadyTimeout    = 5 * time.Minute
	resourceDeletionTimeout = 10 * time.Minute
	pollInterval            = 5 * time.Second
	httpRequestTimeout      = 10 * time.Second
	httpEchoImage           = "hashicorp/http-echo:1.0.0"

	backendContainerPort int32 = 8080
	backendServicePort   int32 = 80
)

const testWAFRule = `SecRule REQUEST_URI "@streq /blocked-by-waf" "id:1000,phase:1,deny,status:403,log,msg:'integration test waf block'"`

type integrationFixture struct {
	suffix           string
	namespaceName    string
	ingressClassName string
}

type stackitTestConfig struct {
	projectID   string
	region      string
	albEndpoint string
}

type ingressPathSpec struct {
	Path        string
	PathType    networkingv1.PathType
	ServiceName string
	ServicePort int32
}

type ingressSpec struct {
	Name          string
	Host          string
	Annotations   map[string]string
	Paths         []ingressPathSpec
	TLSSecretName string
}

type requestOptions struct {
	Scheme             string
	Port               int
	Host               string
	Path               string
	ServerName         string
	InsecureSkipVerify bool
}

type requestResult struct {
	statusCode     int
	body           string
	tlsFingerprint string
	endpoint       string
}

type ipifyResponse struct {
	IP string `json:"ip"`
}

func newIntegrationFixture() *integrationFixture {
	GinkgoHelper()

	suffix := strings.ToLower(fmt.Sprintf("p%d-%s", GinkgoParallelProcess(), uuid.NewString()[:8]))

	return &integrationFixture{
		suffix:           suffix,
		namespaceName:    "alb-it-" + suffix,
		ingressClassName: "alb-it-" + suffix,
	}
}

func (f *integrationFixture) name(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, f.suffix)
}

func (f *integrationFixture) host(prefix string) string {
	return fmt.Sprintf("%s-%s.alb.test", prefix, f.suffix)
}

func (f *integrationFixture) response(prefix string) string {
	return fmt.Sprintf("%s-response-%s", prefix, f.suffix)
}

func stackitServiceAccountKeyOrSkip() string {
	GinkgoHelper()

	serviceAccountKey := strings.TrimSpace(os.Getenv("STACKIT_SA"))
	if serviceAccountKey == "" {
		Fail("STACKIT_SA must contain the STACKIT service account key JSON for the WAF integration test")
	}

	return serviceAccountKey
}

func newStackitTestConfigFromCluster(ctx context.Context) stackitTestConfig {
	GinkgoHelper()

	deployments := &appsv1.DeploymentList{}
	err := k8sClient.List(ctx, deployments, client.MatchingLabels{
		"app.kubernetes.io/name": "application-load-balancer-controller",
	})
	Expect(err).NotTo(HaveOccurred())

	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		configPath, container, found := findControllerCloudConfigPath(deployment)
		if !found {
			continue
		}

		volumeName, relativePath, found := resolveCloudConfigVolumeMount(container, configPath)
		if !found {
			continue
		}

		volume, found := findVolumeByName(deployment.Spec.Template.Spec.Volumes, volumeName)
		if !found || volume.Secret == nil {
			continue
		}

		secretKey := resolveSecretKeyForMount(relativePath, volume.Secret.Items)
		if secretKey == "" {
			continue
		}

		secret := &corev1.Secret{}
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: deployment.Namespace, Name: volume.Secret.SecretName}, secret)
		Expect(err).NotTo(HaveOccurred())

		rawConfig, exists := secret.Data[secretKey]
		if !exists {
			continue
		}

		var cfg stackitconfig.ALBConfig
		err = yaml.Unmarshal(rawConfig, &cfg)
		Expect(err).NotTo(HaveOccurred())

		return stackitTestConfig{
			projectID:   cfg.Global.ProjectID,
			region:      cfg.Global.Region,
			albEndpoint: cfg.Global.APIEndpoints.ApplicationLoadBalancerAPI,
		}
	}

	Fail("could not resolve the controller cloud config secret from the live cluster deployment")
	return stackitTestConfig{}
}

func findControllerCloudConfigPath(deployment *appsv1.Deployment) (string, corev1.Container, bool) {
	GinkgoHelper()

	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, arg := range container.Args {
			if !strings.HasPrefix(arg, "--cloud-config=") {
				continue
			}
			return strings.TrimPrefix(arg, "--cloud-config="), container, true
		}
	}

	return "", corev1.Container{}, false
}

func resolveCloudConfigVolumeMount(container corev1.Container, configPath string) (string, string, bool) {
	GinkgoHelper()

	cleanPath := filepath.Clean(configPath)
	for _, mount := range container.VolumeMounts {
		relPath, err := filepath.Rel(filepath.Clean(mount.MountPath), cleanPath)
		if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			continue
		}
		return mount.Name, filepath.ToSlash(relPath), true
	}

	return "", "", false
}

func findVolumeByName(volumes []corev1.Volume, volumeName string) (corev1.Volume, bool) {
	GinkgoHelper()

	for _, volume := range volumes {
		if volume.Name == volumeName {
			return volume, true
		}
	}

	return corev1.Volume{}, false
}

func resolveSecretKeyForMount(relativePath string, items []corev1.KeyToPath) string {
	GinkgoHelper()

	if len(items) == 0 {
		return relativePath
	}

	for _, item := range items {
		if item.Path == relativePath {
			return item.Key
		}
	}

	return ""
}

func newALBAPIClientForTests(cfg stackitTestConfig, serviceAccountKey string) *albsdk.APIClient {
	GinkgoHelper()

	options := []sdkconfig.ConfigurationOption{
		sdkconfig.WithUserAgent("application-load-balancer-controller-integration-test"),
		sdkconfig.WithServiceAccountKey(serviceAccountKey),
	}
	if cfg.albEndpoint != "" {
		options = append(options, sdkconfig.WithEndpoint(cfg.albEndpoint))
	}

	client, err := albsdk.NewAPIClient(options...)
	Expect(err).NotTo(HaveOccurred())

	return client
}

func newWAFAPIClientForTests(serviceAccountKey string) *albwafsdk.APIClient {
	GinkgoHelper()

	client, err := albwafsdk.NewAPIClient(
		sdkconfig.WithUserAgent("application-load-balancer-controller-integration-test"),
		sdkconfig.WithServiceAccountKey(serviceAccountKey),
	)
	Expect(err).NotTo(HaveOccurred())

	return client
}

func createTestWAF(ctx context.Context, cfg stackitTestConfig, serviceAccountKey string, fixture *integrationFixture) string {
	GinkgoHelper()

	wafClient := newWAFAPIClientForTests(serviceAccountKey)
	rulesConfigName := fixture.name("waf-rules")
	wafName := fixture.name("waf")

	createRulesPayload := albwafsdk.NewCreateRulesPayload()
	createRulesPayload.SetName(rulesConfigName)
	createRulesPayload.SetProjectId(cfg.projectID)
	createRulesPayload.SetRegion(cfg.region)
	createRulesPayload.SetRules(testWAFRule)

	_, err := wafClient.DefaultAPI.CreateRules(ctx, cfg.projectID, cfg.region).
		CreateRulesPayload(*createRulesPayload).
		Execute()
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() error {
		rules, err := wafClient.DefaultAPI.GetRules(ctx, cfg.projectID, cfg.region, rulesConfigName).Execute()
		if err != nil {
			return err
		}
		if rules.GetName() != rulesConfigName {
			return fmt.Errorf("unexpected rules config name %q", rules.GetName())
		}
		return nil
	}, albProvisionTimeout, pollInterval).Should(Succeed())

	createWAFPayload := albwafsdk.NewCreateWAFPayload()
	createWAFPayload.SetName(wafName)
	createWAFPayload.SetProjectId(cfg.projectID)
	createWAFPayload.SetRegion(cfg.region)
	createWAFPayload.SetRulesConfigName(rulesConfigName)

	_, err = wafClient.DefaultAPI.CreateWAF(ctx, cfg.projectID, cfg.region).
		CreateWAFPayload(*createWAFPayload).
		Execute()
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() error {
		waf, err := wafClient.DefaultAPI.GetWAF(ctx, cfg.projectID, cfg.region, wafName).Execute()
		if err != nil {
			return err
		}
		if waf.GetName() != wafName {
			return fmt.Errorf("unexpected WAF name %q", waf.GetName())
		}
		if waf.GetRulesConfigName() != rulesConfigName {
			return fmt.Errorf("unexpected rules config %q", waf.GetRulesConfigName())
		}
		return nil
	}, albProvisionTimeout, pollInterval).Should(Succeed())

	DeferCleanup(func(cleanupCtx context.Context) {
		Eventually(func() error {
			_, err := wafClient.DefaultAPI.DeleteWAF(cleanupCtx, cfg.projectID, cfg.region, wafName).Execute()
			if isNotFoundOpenAPIError(err) || err == nil {
				return nil
			}
			return err
		}, resourceDeletionTimeout, pollInterval).Should(Succeed())

		Eventually(func() error {
			_, err := wafClient.DefaultAPI.DeleteRules(cleanupCtx, cfg.projectID, cfg.region, rulesConfigName).Execute()
			if isNotFoundOpenAPIError(err) || err == nil {
				return nil
			}
			return err
		}, resourceDeletionTimeout, pollInterval).Should(Succeed())
	})

	return wafName
}

func waitForALBListenerWAF(
	ctx context.Context,
	cfg stackitTestConfig,
	serviceAccountKey string,
	ingressClass *networkingv1.IngressClass,
	port int32,
	wafName string,
) {
	GinkgoHelper()

	albClient := newALBAPIClientForTests(cfg, serviceAccountKey)
	loadBalancerName := spec.LoadBalancerName(ingressClass)

	Eventually(func() error {
		loadBalancer, err := albClient.DefaultAPI.GetLoadBalancer(ctx, cfg.projectID, cfg.region, loadBalancerName).Execute()
		if err != nil {
			return err
		}

		for _, listener := range loadBalancer.Listeners {
			if listener.GetPort() != port {
				continue
			}
			if listener.GetWafConfigName() != wafName {
				return fmt.Errorf("listener port %d has unexpected WAF config %q", port, listener.GetWafConfigName())
			}
			return nil
		}

		return fmt.Errorf("listener for port %d not found on ALB %s", port, loadBalancerName)
	}, albProvisionTimeout, pollInterval).Should(Succeed())
}

func waitForALBDeletion(
	ctx context.Context,
	cfg stackitTestConfig,
	serviceAccountKey string,
	ingressClass *networkingv1.IngressClass,
) {
	GinkgoHelper()

	albClient := newALBAPIClientForTests(cfg, serviceAccountKey)
	loadBalancerName := spec.LoadBalancerName(ingressClass)

	Eventually(func() error {
		_, err := albClient.DefaultAPI.GetLoadBalancer(ctx, cfg.projectID, cfg.region, loadBalancerName).Execute()
		if err == nil {
			return fmt.Errorf("ALB %s still exists", loadBalancerName)
		}
		if isNotFoundOpenAPIError(err) {
			return nil
		}
		return err
	}, resourceDeletionTimeout, pollInterval).Should(Succeed())
}

func isNotFoundOpenAPIError(err error) bool {
	GinkgoHelper()

	var openAPIErr *oapierror.GenericOpenAPIError
	return errors.As(err, &openAPIErr) && openAPIErr.GetStatusCode() == http.StatusNotFound
}

func (f *integrationFixture) createNamespace(ctx context.Context) *corev1.Namespace {
	GinkgoHelper()

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: f.namespaceName,
		},
	}
	Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	DeferCleanup(func(cleanupCtx context.Context) {
		deleteObjectAndWait(cleanupCtx, namespace, resourceDeletionTimeout)
	})

	return namespace
}

func (f *integrationFixture) createBackend(ctx context.Context, name, responseBody string) string {
	GinkgoHelper()

	labels := map[string]string{
		"app.kubernetes.io/name":     name,
		"app.kubernetes.io/instance": f.namespaceName,
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.namespaceName,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "echo",
							Image: httpEchoImage,
							Args: []string{
								"-listen=:" + strconv.Itoa(int(backendContainerPort)),
								"-text=" + responseBody,
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: backendContainerPort,
									Name:          "http",
								},
							},
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
	waitForDeploymentAvailable(ctx, client.ObjectKeyFromObject(deployment))

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.namespaceName,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       backendServicePort,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt(int(backendContainerPort)),
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, service)).To(Succeed())

	return service.Name
}

func (f *integrationFixture) createIngressClass(ctx context.Context, annotations map[string]string) *networkingv1.IngressClass {
	GinkgoHelper()

	ingressClassAnnotations := map[string]string{
		annotationNetworkMode: networkModeNodePort,
	}
	for key, value := range annotations {
		ingressClassAnnotations[key] = value
	}

	ingressClass := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        f.ingressClassName,
			Annotations: ingressClassAnnotations,
		},
		Spec: networkingv1.IngressClassSpec{
			Controller: controllerName,
		},
	}
	Expect(k8sClient.Create(ctx, ingressClass)).To(Succeed())
	DeferCleanup(func(cleanupCtx context.Context) {
		deleteObjectAndWait(cleanupCtx, ingressClass, resourceDeletionTimeout)
	})

	return ingressClass
}

func (f *integrationFixture) createIngress(ctx context.Context, spec ingressSpec) *networkingv1.Ingress {
	GinkgoHelper()

	ingressAnnotations := map[string]string{}
	for key, value := range spec.Annotations {
		ingressAnnotations[key] = value
	}

	paths := make([]networkingv1.HTTPIngressPath, 0, len(spec.Paths))
	for _, path := range spec.Paths {
		pathType := path.PathType
		paths = append(paths, networkingv1.HTTPIngressPath{
			Path:     path.Path,
			PathType: &pathType,
			Backend: networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: path.ServiceName,
					Port: networkingv1.ServiceBackendPort{
						Number: path.ServicePort,
					},
				},
			},
		})
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   f.namespaceName,
			Annotations: ingressAnnotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &f.ingressClassName,
			Rules: []networkingv1.IngressRule{
				{
					Host: spec.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: paths,
						},
					},
				},
			},
		},
	}
	if spec.TLSSecretName != "" {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				SecretName: spec.TLSSecretName,
			},
		}
	}

	Expect(k8sClient.Create(ctx, ingress)).To(Succeed())

	return ingress
}

func (f *integrationFixture) createTLSSecret(ctx context.Context, name string, certificatePEM, privateKeyPEM []byte) *corev1.Secret {
	GinkgoHelper()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.namespaceName,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certificatePEM,
			corev1.TLSPrivateKeyKey: privateKeyPEM,
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	return secret
}

func waitForDeploymentAvailable(ctx context.Context, key client.ObjectKey) {
	GinkgoHelper()

	Eventually(func() error {
		deployment := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, key, deployment); err != nil {
			return err
		}
		if deployment.Status.ObservedGeneration < deployment.Generation {
			return fmt.Errorf("deployment %s has not been observed yet", key)
		}
		if deployment.Status.AvailableReplicas < 1 {
			return fmt.Errorf("deployment %s has no available replicas yet", key)
		}
		return nil
	}, resourceReadyTimeout, pollInterval).Should(Succeed())
}

func waitForIngressAddress(ctx context.Context, namespace, name string) string {
	GinkgoHelper()

	key := client.ObjectKey{Namespace: namespace, Name: name}
	address := ""

	Eventually(func() error {
		ingress := &networkingv1.Ingress{}
		if err := k8sClient.Get(ctx, key, ingress); err != nil {
			return err
		}
		if len(ingress.Status.LoadBalancer.Ingress) == 0 {
			return fmt.Errorf("ingress %s has no load balancer address yet", key)
		}
		address = ingress.Status.LoadBalancer.Ingress[0].IP
		if address == "" {
			return fmt.Errorf("ingress %s has no load balancer IP yet", key)
		}
		return nil
	}, albProvisionTimeout, pollInterval).Should(Succeed())

	return address
}

func waitForHTTPResponse(ctx context.Context, address string, port int, host, path, wantBody string, timeout time.Duration) {
	GinkgoHelper()

	waitForResponse(ctx, address, requestOptions{
		Scheme: "http",
		Port:   port,
		Host:   host,
		Path:   path,
	}, timeout, func(response requestResult) error {
		if response.statusCode != http.StatusOK {
			return fmt.Errorf("unexpected HTTP status %d on %s: %s", response.statusCode, response.endpoint, response.body)
		}
		if !strings.Contains(response.body, wantBody) {
			return fmt.Errorf("unexpected HTTP response body %q on %s", response.body, response.endpoint)
		}
		return nil
	})
}

func waitForHTTPStatus(ctx context.Context, address string, port int, host, path string, wantStatus int, timeout time.Duration) {
	GinkgoHelper()

	waitForResponse(ctx, address, requestOptions{
		Scheme: "http",
		Port:   port,
		Host:   host,
		Path:   path,
	}, timeout, func(response requestResult) error {
		if response.statusCode != wantStatus {
			return fmt.Errorf("unexpected HTTP status %d on %s: %s", response.statusCode, response.endpoint, response.body)
		}
		return nil
	})
}

func waitForHTTPUnavailable(ctx context.Context, address string, port int, host, path string, timeout time.Duration) {
	GinkgoHelper()

	Eventually(func() error {
		response, err := performRequest(ctx, address, requestOptions{
			Scheme: "http",
			Port:   port,
			Host:   host,
			Path:   path,
		})
		if err != nil {
			return nil
		}
		if response.statusCode == http.StatusOK {
			return fmt.Errorf("unexpected HTTP 200 on %s: %s", response.endpoint, response.body)
		}
		return nil
	}, timeout, pollInterval).Should(Succeed())
}

func waitForHTTPSUnavailable(ctx context.Context, address string, port int, host, path string, timeout time.Duration) {
	GinkgoHelper()

	Eventually(func() error {
		response, err := performRequest(ctx, address, requestOptions{
			Scheme:             "https",
			Port:               port,
			Host:               host,
			Path:               path,
			ServerName:         host,
			InsecureSkipVerify: true,
		})
		if err != nil {
			return nil
		}
		if response.statusCode == http.StatusOK {
			return fmt.Errorf("unexpected HTTPS 200 on %s: %s", response.endpoint, response.body)
		}
		return nil
	}, timeout, pollInterval).Should(Succeed())
}

func waitForRequestBlocked(ctx context.Context, address string, port int, host, path string, timeout time.Duration) {
	GinkgoHelper()

	Eventually(func() error {
		response, err := performRequest(ctx, address, requestOptions{
			Scheme: "http",
			Port:   port,
			Host:   host,
			Path:   path,
		})
		if err != nil {
			return nil
		}
		if response.statusCode == http.StatusOK {
			return fmt.Errorf("unexpected HTTP 200 on %s while request should be blocked: %s", response.endpoint, response.body)
		}
		return nil
	}, timeout, pollInterval).Should(Succeed())
}

func waitForIngressWarningEvent(ctx context.Context, namespace, name, contains string, timeout time.Duration) {
	GinkgoHelper()

	Eventually(func() error {
		events := &corev1.EventList{}
		if err := k8sClient.List(ctx, events, client.InNamespace(namespace)); err != nil {
			return err
		}

		for _, event := range events.Items {
			if event.InvolvedObject.Kind != "Ingress" ||
				event.InvolvedObject.Namespace != namespace ||
				event.InvolvedObject.Name != name ||
				event.Type != corev1.EventTypeWarning ||
				event.Reason != "IngressWarning" {
				continue
			}
			if strings.Contains(event.Message, contains) {
				return nil
			}
		}

		return fmt.Errorf("warning event for ingress %s/%s containing %q not observed yet", namespace, name, contains)
	}, timeout, pollInterval).Should(Succeed())
}

func waitForHTTPSResponse(ctx context.Context, address string, port int, host, path, wantBody, wantFingerprint string, timeout time.Duration) {
	GinkgoHelper()

	waitForResponse(ctx, address, requestOptions{
		Scheme:             "https",
		Port:               port,
		Host:               host,
		Path:               path,
		ServerName:         host,
		InsecureSkipVerify: true,
	}, timeout, func(response requestResult) error {
		if response.statusCode != http.StatusOK {
			return fmt.Errorf("unexpected HTTPS status %d on %s: %s", response.statusCode, response.endpoint, response.body)
		}
		if !strings.Contains(response.body, wantBody) {
			return fmt.Errorf("unexpected HTTPS response body %q on %s", response.body, response.endpoint)
		}
		if wantFingerprint != "" && response.tlsFingerprint != wantFingerprint {
			return fmt.Errorf("unexpected TLS fingerprint %s on %s", response.tlsFingerprint, response.endpoint)
		}
		return nil
	})
}

func waitForResponse(ctx context.Context, address string, options requestOptions, timeout time.Duration, validate func(requestResult) error) {
	GinkgoHelper()

	Eventually(func() error {
		response, err := performRequest(ctx, address, options)
		if err != nil {
			return err
		}
		return validate(response)
	}, timeout, pollInterval).Should(Succeed())
}

func performRequest(ctx context.Context, address string, options requestOptions) (requestResult, error) {
	endpoint := url.URL{
		Scheme: options.Scheme,
		Host:   net.JoinHostPort(address, strconv.Itoa(options.Port)),
		Path:   options.Path,
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return requestResult{}, err
	}
	request.Host = options.Host

	httpClient := &http.Client{
		Timeout: httpRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:         options.ServerName,
				InsecureSkipVerify: options.InsecureSkipVerify,
			},
		},
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return requestResult{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return requestResult{}, err
	}

	fingerprint := ""
	if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		fingerprint = certificateFingerprint(response.TLS.PeerCertificates[0].Raw)
	}

	return requestResult{
		statusCode:     response.StatusCode,
		body:           string(body),
		tlsFingerprint: fingerprint,
		endpoint:       endpoint.String(),
	}, nil
}

func updateIngress(ctx context.Context, namespace, name string, mutate func(*networkingv1.Ingress)) {
	GinkgoHelper()

	key := client.ObjectKey{Namespace: namespace, Name: name}
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ingress := &networkingv1.Ingress{}
		if err := k8sClient.Get(ctx, key, ingress); err != nil {
			return err
		}
		mutate(ingress)
		return k8sClient.Update(ctx, ingress)
	})).To(Succeed())
}

func updateIngressClass(ctx context.Context, name string, mutate func(*networkingv1.IngressClass)) {
	GinkgoHelper()

	key := client.ObjectKey{Name: name}
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ingressClass := &networkingv1.IngressClass{}
		if err := k8sClient.Get(ctx, key, ingressClass); err != nil {
			return err
		}
		mutate(ingressClass)
		return k8sClient.Update(ctx, ingressClass)
	})).To(Succeed())
}

func updateIngressHTTPPort(ctx context.Context, namespace, name string, port int) {
	GinkgoHelper()

	updateIngress(ctx, namespace, name, func(ingress *networkingv1.Ingress) {
		if ingress.Annotations == nil {
			ingress.Annotations = map[string]string{}
		}
		ingress.Annotations[annotationHTTPPort] = strconv.Itoa(port)
	})
}

func updateTLSSecret(ctx context.Context, namespace, name string, certificatePEM, privateKeyPEM []byte) {
	GinkgoHelper()

	key := client.ObjectKey{Namespace: namespace, Name: name}
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, key, secret); err != nil {
			return err
		}
		secret.Data[corev1.TLSCertKey] = certificatePEM
		secret.Data[corev1.TLSPrivateKeyKey] = privateKeyPEM
		return k8sClient.Update(ctx, secret)
	})).To(Succeed())
}

func deleteObjectAndWait(ctx context.Context, obj client.Object, timeout time.Duration) {
	GinkgoHelper()

	key := client.ObjectKeyFromObject(obj)
	err := k8sClient.Delete(ctx, obj)
	if err != nil && !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	Eventually(func() error {
		err := k8sClient.Get(ctx, key, obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("object %T %s still exists", obj, key)
	}, timeout, pollInterval).Should(Succeed(), "expected %T %s to be deleted", obj, key)
}

func discoverRunnerPublicIP(ctx context.Context) string {
	GinkgoHelper()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org?format=json", nil)
	Expect(err).NotTo(HaveOccurred())

	client := &http.Client{Timeout: httpRequestTimeout}
	response, err := client.Do(request)
	Expect(err).NotTo(HaveOccurred(), "expected to fetch runner public IP from api.ipify.org")
	defer response.Body.Close()

	Expect(response.StatusCode).To(Equal(http.StatusOK), "expected api.ipify.org to return 200")

	decoder := json.NewDecoder(response.Body)
	var payload ipifyResponse
	Expect(decoder.Decode(&payload)).To(Succeed())

	ip := net.ParseIP(payload.IP)
	Expect(ip).NotTo(BeNil(), "expected api.ipify.org to return a valid IP address")

	return ip.String()
}

func toSingleHostCIDR(ip string) string {
	GinkgoHelper()

	parsed := net.ParseIP(ip)
	Expect(parsed).NotTo(BeNil(), "expected a valid IP address")
	if parsed.To4() != nil {
		return parsed.String() + "/32"
	}
	return parsed.String() + "/128"
}

func generateSelfSignedCertificate(host string) ([]byte, []byte, string) {
	GinkgoHelper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	Expect(err).NotTo(HaveOccurred())

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{host},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	Expect(err).NotTo(HaveOccurred())

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	Expect(err).NotTo(HaveOccurred())

	certificatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificateDER,
	})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyDER,
	})

	return certificatePEM, privateKeyPEM, certificateFingerprint(certificateDER)
}

func certificateFingerprint(rawCertificate []byte) string {
	sum := sha256.Sum256(rawCertificate)
	return hex.EncodeToString(sum[:])
}

func int32Ptr(value int32) *int32 {
	return &value
}
