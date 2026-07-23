// Package webhook provides validating admission webhooks for STACKIT ALB Ingress and IngressClass resources.
package webhook

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	admissionv1 "k8s.io/api/admission/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
)

// wafNameRegex validates the WAF name annotation.
// The name must start and end with a lowercase alphanumeric character,
// contain only lowercase alphanumeric characters or hyphens, and be
// between 1 and 63 characters long.
var wafNameRegex = regexp.MustCompile(`^[0-9a-z](?:(?:[0-9a-z]|-){0,61}[0-9a-z])?$`)

// IngressValidator is a validating admission webhook for Ingress resources.
// It only validates ingresses that reference an IngressClass managed by the STACKIT ALB controller.
type IngressValidator struct {
	Client  client.Reader
	Decoder admission.Decoder
}

// Handle routes the admission request based on the operation type.
//
//nolint:gocritic // admission.Request is passed by value to satisfy the admission.Handler interface
func (v *IngressValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case admissionv1.Create, admissionv1.Update:
		return v.validate(ctx, req)
	default:
		return admission.Allowed("unhandled operation allowed")
	}
}

//nolint:gocritic // admission.Request is passed by value to match the interface convention
func (v *IngressValidator) validate(ctx context.Context, req admission.Request) admission.Response {
	ing := &networkingv1.Ingress{}
	if err := v.Decoder.Decode(req, ing); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if ing.Spec.IngressClassName == nil {
		return admission.Allowed("no ingress class specified; ignoring")
	}

	ingressClass := &networkingv1.IngressClass{}
	// TODO: How do we deal with 404s? The ingress class might be created just moments later.
	if err := v.Client.Get(ctx, client.ObjectKey{Name: *ing.Spec.IngressClassName}, ingressClass); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if ingressClass.Spec.Controller != ingress.ControllerName {
		return admission.Allowed("ingress managed by a different controller; allowing")
	}

	if err := validateIngressAnnotations(ing); err != nil {
		return admission.Denied(err.Error())
	}

	return admission.Allowed("validation passed")
}

// validateIngressAnnotations checks formatting, allowed values, and basic constraints
// for annotations that may be set on an Ingress managed by the STACKIT ALB controller.
func validateIngressAnnotations(ing *networkingv1.Ingress) error {
	if val, ok := ing.Annotations[spec.AnnotationWAFName]; ok {
		if !wafNameRegex.MatchString(val) {
			return fmt.Errorf("annotation %q has an invalid value %q: must match %s",
				spec.AnnotationWAFName, val, wafNameRegex.String())
		}
	}

	boolAnnotations := []string{
		spec.AnnotationTargetPoolTLSEnabled,
		spec.AnnotationTargetPoolTLSSkipCertificateValidation,
		spec.AnnotationHTTPSOnly,
		spec.AnnotationWebSocket,
	}
	for _, ann := range boolAnnotations {
		if val, ok := ing.Annotations[ann]; ok {
			if _, err := strconv.ParseBool(val); err != nil {
				return fmt.Errorf("annotation %q must be a valid boolean: %w", ann, err)
			}
		}
	}

	if val, ok := ing.Annotations[spec.AnnotationPriority]; ok {
		if _, err := strconv.Atoi(val); err != nil {
			return fmt.Errorf("annotation %q must be a valid integer: %w", spec.AnnotationPriority, err)
		}
	}

	for _, ann := range []string{spec.AnnotationHTTPPort, spec.AnnotationHTTPSPort} {
		if val, ok := ing.Annotations[ann]; ok {
			port, err := strconv.Atoi(val)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("annotation %q must be a valid port number between 1 and 65535", ann)
			}
		}
	}

	return nil
}
