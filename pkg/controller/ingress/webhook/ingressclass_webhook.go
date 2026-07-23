package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	admissionv1 "k8s.io/api/admission/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
)

// IngressClassValidator is a validating admission webhook for IngressClass resources
// managed by the STACKIT ALB controller.
type IngressClassValidator struct {
	Client  client.Reader
	Decoder admission.Decoder
}

// Handle routes the admission request based on the operation type.
//
//nolint:gocritic // admission.Request is passed by value to satisfy the admission.Handler interface
func (v *IngressClassValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case admissionv1.Create:
		return v.handleCreate(ctx, req)
	case admissionv1.Update:
		return v.handleUpdate(ctx, req)
	default:
		return admission.Allowed("unhandled operation allowed")
	}
}

//nolint:gocritic // admission.Request is passed by value to match the interface convention
func (v *IngressClassValidator) handleCreate(_ context.Context, req admission.Request) admission.Response {
	newClass := &networkingv1.IngressClass{}
	if err := v.Decoder.Decode(req, newClass); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if newClass.Spec.Controller != ingress.ControllerName {
		return admission.Allowed("not a STACKIT ALB IngressClass; allowing")
	}

	if err := validateIngressClassAnnotations(newClass); err != nil {
		return admission.Denied(err.Error())
	}

	return admission.Allowed("validation passed")
}

//nolint:gocritic // admission.Request is passed by value to match the interface convention
func (v *IngressClassValidator) handleUpdate(ctx context.Context, req admission.Request) admission.Response {
	newClass := &networkingv1.IngressClass{}
	if err := v.Decoder.Decode(req, newClass); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	oldClass := &networkingv1.IngressClass{}
	if err := v.Decoder.DecodeRaw(req.OldObject, oldClass); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if newClass.Spec.Controller != ingress.ControllerName {
		return admission.Allowed("not a STACKIT ALB IngressClass; allowing")
	}

	if err := validateIngressClassAnnotations(newClass); err != nil {
		return admission.Denied(err.Error())
	}

	// AnnotationInternal is immutable after creation.
	oldInternal := oldClass.Annotations[spec.AnnotationInternal]
	newInternal := newClass.Annotations[spec.AnnotationInternal]
	if oldInternal != newInternal {
		return admission.Denied(fmt.Sprintf("annotation %q is immutable and cannot be changed after creation",
			spec.AnnotationInternal))
	}

	if err := v.validateExternalIPUpdate(ctx, oldClass, newClass); err != nil {
		return admission.Denied(err.Error())
	}

	return admission.Allowed("validation passed")
}

// validateIngressClassAnnotations checks formatting, allowed values, and basic constraints
// for annotations that may be set on an IngressClass managed by the STACKIT ALB controller.
//
//nolint:gocyclo // Straightforward sequence of independent annotation checks.
func validateIngressClassAnnotations(class *networkingv1.IngressClass) error {
	if err := spec.ValidateNetworkMode(class); err != nil {
		return err
	}

	if val, ok := class.Annotations[spec.AnnotationExternalIP]; ok {
		if err := spec.ValidateExternalIP(val); err != nil {
			return err
		}
	}

	if val, ok := class.Annotations[spec.AnnotationInternal]; ok {
		if _, err := strconv.ParseBool(val); err != nil {
			return fmt.Errorf("annotation %q must be a valid boolean: %w", spec.AnnotationInternal, err)
		}
	}

	if val, ok := class.Annotations[spec.AnnotationPlanID]; ok {
		if err := spec.ValidatePlanID(val); err != nil {
			return err
		}
	}

	for _, ann := range []string{spec.AnnotationHTTPPort, spec.AnnotationHTTPSPort} {
		if val, ok := class.Annotations[ann]; ok {
			port, err := strconv.Atoi(val)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("annotation %q must be a valid port number between 1 and 65535", ann)
			}
		}
	}

	if val, ok := class.Annotations[spec.AnnotationAllowedSourceRanges]; ok {
		if _, err := spec.ValidateAllowedSourceRanges(val); err != nil {
			return err
		}
	}

	if val, ok := class.Annotations[spec.AnnotationWAFName]; ok {
		if !wafNameRegex.MatchString(val) {
			return fmt.Errorf("annotation %q has an invalid value %q: must match %s",
				spec.AnnotationWAFName, val, wafNameRegex.String())
		}
	}

	return nil
}

// validateExternalIPUpdate enforces the update rules for the external IP annotation:
//   - Changing an existing static IP is not allowed.
//   - Promoting an ephemeral IP to a static IP is only allowed when the requested static
//     IP matches the currently assigned ephemeral IP.
func (v *IngressClassValidator) validateExternalIPUpdate(ctx context.Context, oldClass, newClass *networkingv1.IngressClass) error {
	oldIP, oldHadIP := oldClass.Annotations[spec.AnnotationExternalIP]
	newIP, newHasIP := newClass.Annotations[spec.AnnotationExternalIP]

	if oldHadIP && newHasIP && oldIP != newIP {
		return fmt.Errorf(
			"changing an existing static IP address is not allowed: annotation %q cannot be updated from %q to %q",
			spec.AnnotationExternalIP, oldIP, newIP,
		)
	}

	if !oldHadIP && newHasIP {
		currentIP, err := v.getAssignedEphemeralIP(ctx, newClass.Name)
		if err != nil {
			return fmt.Errorf("failed to look up currently assigned IP: %w", err)
		}
		if currentIP == "" || currentIP != newIP {
			return fmt.Errorf(
				"the load balancer can only be promoted to a static IP address that matches its current ephemeral IP (currently assigned: %q, requested: %q)",
				currentIP, newIP,
			)
		}
	}

	return nil
}

// getAssignedEphemeralIP scans the cluster for any Ingress that references the given class
// and returns the first IP reported in its status. Returns an empty string if no IP is
// currently assigned.
func (v *IngressClassValidator) getAssignedEphemeralIP(ctx context.Context, className string) (string, error) {
	ingressList := &networkingv1.IngressList{}
	if err := v.Client.List(ctx, ingressList); err != nil {
		return "", err
	}
	for i := range ingressList.Items {
		ing := &ingressList.Items[i]
		if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != className {
			continue
		}
		for _, lb := range ing.Status.LoadBalancer.Ingress {
			if lb.IP != "" {
				return lb.IP, nil
			}
		}
	}
	return "", nil
}
