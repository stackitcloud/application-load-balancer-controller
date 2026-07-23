package spec

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
)

// ServicePlans lists all supported ALB service plans.
var ServicePlans = []string{
	"p10",
}

// DefaultServicePlan is the plan used when no plan is explicitly requested.
const DefaultServicePlan = "p10"

// ValidateNetworkMode validates that the network mode annotation is set to a supported value.
// Currently only NetworkModeNodePort is supported and the annotation is mandatory.
func ValidateNetworkMode(ingressClass *networkingv1.IngressClass) error {
	networkMode := ingressClass.Annotations[AnnotationNetworkMode]
	if networkMode != NetworkModeNodePort {
		return fmt.Errorf("annotation %s must be set to %s", AnnotationNetworkMode, NetworkModeNodePort)
	}
	return nil
}

// ValidateExternalIP validates that the provided external IP annotation value is a valid IPv4 address.
// An empty value is considered valid.
func ValidateExternalIP(value string) error {
	if value == "" {
		return nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return fmt.Errorf("failed to parse external IP annotation: %w", err)
	}
	if !addr.Is4() {
		return fmt.Errorf("external IP annotation is not an IPv4 address")
	}
	return nil
}

// ValidatePlanID validates that the provided plan ID is one of the supported service plans.
// An empty value is considered valid (defaults to DefaultServicePlan at reconcile time).
func ValidatePlanID(value string) error {
	if value == "" {
		return nil
	}
	if !slices.Contains(ServicePlans, value) {
		return fmt.Errorf("invalid plan id %q", value)
	}
	return nil
}

// ValidateAllowedSourceRanges validates a comma-separated list of CIDR ranges.
// An empty value is considered valid.
func ValidateAllowedSourceRanges(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	ranges := strings.Split(value, ",")
	for i, r := range ranges {
		if k := slices.Index(ranges, r); k < i {
			return nil, fmt.Errorf("duplicate range in annotation %s", AnnotationAllowedSourceRanges)
		}
		if _, _, err := net.ParseCIDR(r); err != nil {
			return nil, fmt.Errorf("IP range %d is invalid: %w", i, err)
		}
	}
	return ranges, nil
}
