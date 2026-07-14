package spec

import (
	"fmt"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// AnnotationNetworkMode defines how the traffic from the application load balancer enters the Kubernetes cluster.
	// Currently, the only support value is "NodePort".
	// Must be set on IngressClass.
	AnnotationNetworkMode = "alb.stackit.cloud/network-mode"
	NetworkModeNodePort   = "NodePort"

	// AnnotationExternalIP references a STACKIT public IP that should be used by the application load balancer.
	// If set it will be used instead of an ephemeral IP. The IP must be created by the customer. When the service is deleted,
	// the public IP will not be deleted. The IP is ignored if the alb.stackit.cloud/internal-alb is set.
	// If the annotation is set after the creation it must match the ephemeral IP.
	// This will promote the ephemeral IP to a static IP.
	// Can be set on IngressClass.
	AnnotationExternalIP = "alb.stackit.cloud/external-address"
	// AnnotationInternal If true, the application load balancer is not exposed via a public IP.
	// Can be set on IngressClass.
	AnnotationInternal = "alb.stackit.cloud/internal-alb"
	// AnnotationPlanID sets the plan for the ALB.
	// Can be set on IngressClass.
	AnnotationPlanID = "alb.stackit.cloud/plan-id"

	// AnnotationTargetPoolTLSEnabled If true, the application load balancer enables TLS bridging.
	// It uses the trusted CAs from the operating system for validation.
	// Can be set on IngressClass, Ingress and Service.
	AnnotationTargetPoolTLSEnabled = "alb.stackit.cloud/target-pool-tls-enabled"
	// AnnotationTargetPoolTLSCustomCa If set, the application load balancer enables TLS bridging with a custom CA provided as value.
	// The value must contain a PEM-encoded certificate that is a certificate authority.
	// Can be set on IngressClass, Ingress and Service
	AnnotationTargetPoolTLSCustomCa = "alb.stackit.cloud/target-pool-tls-custom-ca"
	// AnnotationTargetPoolTLSSkipCertificateValidation If true, the application load balancer enables TLS bridging but skips validation.
	// Can be set on IngressClass, Ingress and Service.
	AnnotationTargetPoolTLSSkipCertificateValidation = "alb.stackit.cloud/target-pool-tls-skip-certificate-validation"

	// AnnotationHTTPPort Specifies the HTTP port.
	// Can be set on IngressClass and Ingress.
	AnnotationHTTPPort = "alb.stackit.cloud/http-port"
	// AnnotationHTTPSPort Specifies the HTTPS port.
	// Can be set on IngressClass and Ingress.
	AnnotationHTTPSPort = "alb.stackit.cloud/https-port"
	// AnnotationHTTPSOnly if true, the ingress will not be reachable via HTTP.
	// Setting this to true requires that the ingress has a TLS certificate.
	// Can be set on IngressClass and Ingress.
	AnnotationHTTPSOnly = "alb.stackit.cloud/https-only"

	// AnnotationWebSocket accepts a bool to decide whether websocket support is enabled.
	// Can be set on IngressClass and Ingress.
	AnnotationWebSocket = "alb.stackit.cloud/websocket"

	// AnnotationWAFName accepts a string and must reference a web application firewall that already exists.
	// Can be set on IngressClass and applies to all ports.
	AnnotationWAFName = "alb.stackit.cloud/web-application-firewall-name"

	// AnnotationPriority is used to set the priority of the Ingress. Can be only set on ingress objects.
	// Can be set on IngressClass and Ingress.
	AnnotationPriority = "alb.stackit.cloud/priority"

	// AnnotationAllowedSourceRanges accept a comma-separated list of IP ranges. E.g. 10.0.0.0/24,1.2.3.4/32.
	// Can be set on IngressClass and applies to all ports.
	AnnotationAllowedSourceRanges = "alb.stackit.cloud/allowed-source-ranges"
)

// GetAnnotation retrieves an annotation value from objects.
// If multiple objects contain the annotation, the first object containing the annotation takes precedence.
// If no object contains the annotation then defaultValue is returned.
//
// GetAnnotation parses the value of the annotation and return type T.
// If T is string then the value is returned raw.
// For int and bool Atoi and ParseBool are called respectively.
// If parsing fails, an error is returned together with default value.
// Only the first found value is parsed.
//
// GetAnnotation panics if T is neither a string, int or bool.
func GetAnnotation[T any](annotation string, defaultValue T, objects ...client.Object) (T, error) {
	var rawVal string
	var found bool

	// Iterate through sources (e.g., Ingress, then IngressClass)
	for _, object := range objects {
		if val, exists := object.GetAnnotations()[annotation]; exists {
			rawVal = val
			found = true
			break
		}
	}

	if !found {
		return defaultValue, nil
	}

	var result any
	var err error

	switch any(defaultValue).(type) {
	case string:
		return any(rawVal).(T), nil
	case int:
		result, err = strconv.Atoi(rawVal)
	case bool:
		result, err = strconv.ParseBool(rawVal)
	default:
		return defaultValue, fmt.Errorf("invalid type for GetAnnotation")
	}

	if err != nil {
		return defaultValue, err
	}

	return result.(T), nil
}
