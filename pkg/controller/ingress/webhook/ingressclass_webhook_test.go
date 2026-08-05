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

var _ = Describe("IngressClassValidator", func() {
	const (
		testClassName       = "test-class"
		managedController   = ingress.ControllerName
		unmanagedController = "k8s.io/ingress-nginx"
	)

	newValidator := func(ephemeralIP string) *ingresswebhook.IngressClassValidator {
		builder := fake.NewClientBuilder().WithScheme(testScheme)
		if ephemeralIP != "" {
			builder = builder.WithObjects(&networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "dummy-ingress", Namespace: "default"},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To(testClassName),
				},
				Status: networkingv1.IngressStatus{
					LoadBalancer: networkingv1.IngressLoadBalancerStatus{
						Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: ephemeralIP}},
					},
				},
			})
		}
		return &ingresswebhook.IngressClassValidator{
			Client:  builder.Build(),
			Decoder: admission.NewDecoder(testScheme),
		}
	}

	handle := func(
		v *ingresswebhook.IngressClassValidator,
		operation admissionv1.Operation,
		controller string,
		oldAnnotations, newAnnotations map[string]string,
	) admission.Response {
		newClass := &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{Name: testClassName, Annotations: newAnnotations},
			Spec:       networkingv1.IngressClassSpec{Controller: controller},
		}
		rawNew, err := json.Marshal(newClass)
		Expect(err).ToNot(HaveOccurred())

		req := admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: operation,
				Object:    runtime.RawExtension{Raw: rawNew},
			},
		}
		if operation == admissionv1.Update {
			oldClass := &networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{Name: testClassName, Annotations: oldAnnotations},
				Spec:       networkingv1.IngressClassSpec{Controller: controller},
			}
			rawOld, err := json.Marshal(oldClass)
			Expect(err).ToNot(HaveOccurred())
			req.OldObject = runtime.RawExtension{Raw: rawOld}
		}
		return v.Handle(context.Background(), req)
	}

	DescribeTable("Create",
		func(controller string, annotations map[string]string, expectAllowed bool) {
			res := handle(newValidator(""), admissionv1.Create, controller, nil, annotations)
			Expect(res.Allowed).To(Equal(expectAllowed),
				"unexpected result, message: %s", resultMessage(res))
		},
		Entry("valid IngressClass", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationPlanID:      "p10",
			spec.AnnotationHTTPPort:    "80",
		}, true),
		Entry("unmanaged controller - allowed even with invalid values",
			unmanagedController, map[string]string{spec.AnnotationPlanID: "invalid-plan"}, true),
		Entry("missing network-mode - denied", managedController, map[string]string{
			spec.AnnotationPlanID: "p10",
		}, false),
		Entry("invalid network-mode - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: "LoadBalancer",
		}, false),
		Entry("invalid IP address - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationExternalIP:  "not-an-ip-address",
		}, false),
		Entry("IPv6 address - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationExternalIP:  "2001:db8::1",
		}, false),
		Entry("valid IPv4 - allowed", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationExternalIP:  "1.2.3.4",
		}, true),
		Entry("invalid plan-id - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationPlanID:      "p100",
		}, false),
		Entry("invalid port - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationHTTPPort:    "99999",
		}, false),
		Entry("valid allowed-source-ranges - allowed", managedController, map[string]string{
			spec.AnnotationNetworkMode:         spec.NetworkModeNodePort,
			spec.AnnotationAllowedSourceRanges: "10.0.0.0/24,192.168.0.0/16",
		}, true),
		Entry("invalid allowed-source-ranges - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode:         spec.NetworkModeNodePort,
			spec.AnnotationAllowedSourceRanges: "10.0.0.0/24,not-a-cidr",
		}, false),
		Entry("invalid internal (bool) - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationInternal:    "maybe",
		}, false),
		Entry("valid WAF name - allowed", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationWAFName:     "my-waf",
		}, true),
		Entry("invalid WAF name - denied", managedController, map[string]string{
			spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
			spec.AnnotationWAFName:     "My-WAF",
		}, false),
	)

	DescribeTable("Update",
		func(oldAnn, newAnn map[string]string, ephemeralIP string, expectAllowed bool) {
			res := handle(newValidator(ephemeralIP), admissionv1.Update, managedController, oldAnn, newAnn)
			Expect(res.Allowed).To(Equal(expectAllowed),
				"unexpected result, message: %s", resultMessage(res))
		},
		Entry("keep same static IP - allowed",
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "1.2.3.4",
			},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "1.2.3.4",
			}, "", true),
		Entry("change existing static IP - denied",
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "1.2.3.4",
			},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "5.6.7.8",
			}, "", false),
		Entry("promote ephemeral to static (match) - allowed",
			map[string]string{spec.AnnotationNetworkMode: spec.NetworkModeNodePort},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "9.9.9.9",
			}, "9.9.9.9", true),
		Entry("promote ephemeral to static (mismatch) - denied",
			map[string]string{spec.AnnotationNetworkMode: spec.NetworkModeNodePort},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "8.8.8.8",
			}, "9.9.9.9", false),
		Entry("promote ephemeral to static (no IP assigned) - denied",
			map[string]string{spec.AnnotationNetworkMode: spec.NetworkModeNodePort},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationExternalIP:  "8.8.8.8",
			}, "", false),
		Entry("change internal annotation - denied",
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationInternal:    "true",
			},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationInternal:    "false",
			}, "", false),
		Entry("keep internal annotation - allowed",
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationInternal:    "true",
			},
			map[string]string{
				spec.AnnotationNetworkMode: spec.NetworkModeNodePort,
				spec.AnnotationInternal:    "true",
			}, "", true),
	)
})
