package webhook_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
	ingresswebhook "github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/webhook"
)

var _ = Describe("IngressValidator", func() {
	const (
		managedClass   = "stackit-alb"
		unmanagedClass = "nginx"
	)

	var (
		validator *ingresswebhook.IngressValidator
	)

	BeforeEach(func() {
		managed := &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{Name: managedClass},
			Spec:       networkingv1.IngressClassSpec{Controller: ingress.ControllerName},
		}
		unmanaged := &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{Name: unmanagedClass},
			Spec:       networkingv1.IngressClassSpec{Controller: "k8s.io/ingress-nginx"},
		}
		c := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(managed, unmanaged).Build()
		validator = &ingresswebhook.IngressValidator{
			Client:  c,
			Decoder: admission.NewDecoder(testScheme),
		}
	})

	DescribeTable("Handle",
		func(operation admissionv1.Operation, className *string, annotations map[string]string, expectAllowed bool) {
			ing := &networkingv1.Ingress{
				TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-ingress",
					Namespace:   "default",
					Annotations: annotations,
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: className,
				},
			}
			raw, err := json.Marshal(ing)
			Expect(err).ToNot(HaveOccurred())

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: operation,
					Object:    runtime.RawExtension{Raw: raw},
				},
			}
			if operation == admissionv1.Update {
				req.OldObject = runtime.RawExtension{Raw: raw}
			}

			res := validator.Handle(context.Background(), req)
			Expect(res.Allowed).To(Equal(expectAllowed),
				"unexpected result, message: %s", resultMessage(res))
		},
		Entry("valid ingress (create)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{
				spec.AnnotationHTTPSOnly: "true",
				spec.AnnotationPriority:  "100",
			}, true),
		Entry("valid ingress (update)", admissionv1.Update, ptr.To(managedClass),
			map[string]string{
				spec.AnnotationHTTPSOnly: "false",
			}, true),
		Entry("no ingress class - allowed", admissionv1.Create, nil,
			map[string]string{}, true),
		Entry("unmanaged ingress class - allowed", admissionv1.Create, ptr.To(unmanagedClass),
			map[string]string{spec.AnnotationHTTPSOnly: "not-a-bool"}, true),
		Entry("invalid boolean (https-only)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationHTTPSOnly: "not-a-bool"}, false),
		Entry("invalid boolean (websocket)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWebSocket: "yes"}, false),
		Entry("invalid boolean (tls-enabled)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationTargetPoolTLSEnabled: "on"}, false),
		Entry("invalid boolean (tls-skip)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationTargetPoolTLSSkipCertificateValidation: "on"}, false),
		Entry("invalid integer (priority)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationPriority: "high"}, false),
		Entry("valid WAF name", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "my-valid-waf-123"}, true),
		Entry("valid WAF name (single char)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "a"}, true),
		Entry("invalid WAF name (uppercase)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "My-Waf-Name"}, false),
		Entry("invalid WAF name (leading hyphen)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "-my-waf"}, false),
		Entry("invalid WAF name (trailing hyphen)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "my-waf-"}, false),
		Entry("invalid WAF name (underscore)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "my_waf_name"}, false),
		Entry("invalid WAF name (too long)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationWAFName: "a123456789012345678901234567890123456789012345678901234567890123"}, false),
		Entry("valid HTTP port", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationHTTPPort: "8080"}, true),
		Entry("invalid HTTP port (out of range)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationHTTPPort: "70000"}, false),
		Entry("invalid HTTP port (not a number)", admissionv1.Create, ptr.To(managedClass),
			map[string]string{spec.AnnotationHTTPPort: "abc"}, false),
	)
})

func resultMessage(res admission.Response) string { //nolint:gocritic // admission.Response is the return type of Handle
	if res.Result == nil {
		return ""
	}
	return res.Result.Message
}
