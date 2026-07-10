package spec

import (
	"fmt"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
)

// LoadBalancerName returns the desired name for a load balancer.
// The ingress class must have a UID.
func LoadBalancerName(ingressClass *networkingv1.IngressClass) string {
	name := fmt.Sprintf("k8s-ingress-%s-", ingressClass.UID)
	avail := 63 - len(name)
	if len(ingressClass.Name) <= avail {
		name += ingressClass.Name
	} else {
		name += ingressClass.Name[:avail]
		// Load balancer names must be DNS-compatible, which disallows trailing dashes.
		// By cutting the name in the middle, we might have a trailing dash.
		// By trimming it, we still produce a non-empty valid name.
		name = strings.TrimRight(name, "-")
	}
	return name
}
