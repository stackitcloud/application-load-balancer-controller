package diff

import (
	"maps"
	"slices"

	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	"k8s.io/utils/ptr"
)

// UpdateNeeded return true if fields have change that requires the ALB to be updated.
// The order of slices matter, i.e. swapping two entries makes this function return true.
func UpdateNeeded(alb *albsdk.LoadBalancer, albPayload *albsdk.UpdateLoadBalancerPayload) bool {
	return listenersChanged(alb.Listeners, albPayload.Listeners) ||
		targetPoolsChanged(alb.TargetPools, albPayload.TargetPools) ||
		optionsChanged(alb.Options, albPayload.Options) ||
		labelsChanged(ptr.Deref(alb.Labels, map[string]string{}), ptr.Deref(albPayload.Labels, map[string]string{})) ||
		ptr.Deref(alb.PlanId, "") != ptr.Deref(albPayload.PlanId, "")
}

func labelsChanged(c, d map[string]string) bool {
	return !maps.Equal(c, d)
}

func listenersChanged(current, desired []albsdk.Listener) bool {
	if len(current) != len(desired) {
		return true
	}
	for i := range current {
		c, d := current[i], desired[i]

		if ptr.Deref(c.Name, "") != ptr.Deref(d.Name, "") ||
			ptr.Deref(c.Protocol, "") != ptr.Deref(d.Protocol, "") ||
			ptr.Deref(c.Port, 0) != ptr.Deref(d.Port, 0) ||
			ptr.Deref(c.WafConfigName, "") != ptr.Deref(d.WafConfigName, "") {
			return true
		}

		if httpOptionsChanged(c.Http, d.Http) || httpsOptionsChanged(c.Https, d.Https) {
			return true
		}
	}
	return false
}

func httpOptionsChanged(c, d *albsdk.ProtocolOptionsHTTP) bool {
	if c == nil && d == nil {
		return false
	}
	if c == nil || d == nil || len(c.Hosts) != len(d.Hosts) {
		return true
	}

	for i := range c.Hosts {
		ch, dh := c.Hosts[i], d.Hosts[i]
		if ptr.Deref(ch.Host, "") != ptr.Deref(dh.Host, "") || len(ch.Rules) != len(dh.Rules) {
			return true
		}

		for j := range ch.Rules {
			cr, dr := ch.Rules[j], dh.Rules[j]
			if pathChanged(cr.Path, dr.Path) {
				return true
			}
			if ptr.Deref(cr.WebSocket, false) != ptr.Deref(dr.WebSocket, false) ||
				ptr.Deref(cr.TargetPool, "") != ptr.Deref(dr.TargetPool, "") {
				return true
			}
		}
	}
	return false
}

func pathChanged(c, d *albsdk.Path) bool {
	if c == nil && d == nil {
		return false
	}
	if c == nil || d == nil {
		return true
	}
	return ptr.Deref(c.Prefix, "") != ptr.Deref(d.Prefix, "") || ptr.Deref(c.ExactMatch, "") != ptr.Deref(d.ExactMatch, "")
}

func httpsOptionsChanged(c, d *albsdk.ProtocolOptionsHTTPS) bool {
	return !slices.Equal(
		ptr.Deref(ptr.Deref(c, albsdk.ProtocolOptionsHTTPS{}).CertificateConfig, albsdk.CertificateConfig{}).CertificateIds,
		ptr.Deref(ptr.Deref(d, albsdk.ProtocolOptionsHTTPS{}).CertificateConfig, albsdk.CertificateConfig{}).CertificateIds,
	)
}

func targetPoolsChanged(current, desired []albsdk.TargetPool) bool {
	if len(current) != len(desired) {
		return true
	}
	for i := range current {
		c, d := current[i], desired[i]

		if ptr.Deref(c.Name, "") != ptr.Deref(d.Name, "") ||
			ptr.Deref(c.TargetPort, 0) != ptr.Deref(d.TargetPort, 0) ||
			targetsChanged(c.Targets, d.Targets) {
			return true
		}

		if tlsChanged(c.TlsConfig, d.TlsConfig) {
			return true
		}

		if healthCheckChanged(c.ActiveHealthCheck, d.ActiveHealthCheck) {
			return true
		}
	}
	return false
}

func tlsChanged(c, d *albsdk.TlsConfig) bool {
	cTLS := ptr.Deref(c, albsdk.TlsConfig{})
	dTLS := ptr.Deref(d, albsdk.TlsConfig{})
	if ptr.Deref(cTLS.Enabled, false) != ptr.Deref(dTLS.Enabled, false) ||
		ptr.Deref(cTLS.SkipCertificateValidation, false) != ptr.Deref(dTLS.SkipCertificateValidation, false) ||
		ptr.Deref(cTLS.CustomCa, "") != ptr.Deref(dTLS.CustomCa, "") {
		return true
	}
	return false
}

func healthCheckChanged(c, d *albsdk.ActiveHealthCheck) bool {
	cHealthCheck := ptr.Deref(c, albsdk.ActiveHealthCheck{})
	dHealthCheck := ptr.Deref(d, albsdk.ActiveHealthCheck{})

	if ptr.Deref(cHealthCheck.AltPort, 0) != ptr.Deref(dHealthCheck.AltPort, 0) ||
		ptr.Deref(cHealthCheck.HealthyThreshold, 0) != ptr.Deref(dHealthCheck.HealthyThreshold, 0) ||
		ptr.Deref(cHealthCheck.Interval, "") != ptr.Deref(dHealthCheck.Interval, "") ||
		ptr.Deref(cHealthCheck.IntervalJitter, "") != ptr.Deref(dHealthCheck.IntervalJitter, "") ||
		ptr.Deref(cHealthCheck.Timeout, "") != ptr.Deref(dHealthCheck.Timeout, "") ||
		ptr.Deref(cHealthCheck.UnhealthyThreshold, 0) != ptr.Deref(dHealthCheck.UnhealthyThreshold, 0) {
		return true
	}

	cHTTPHealth := ptr.Deref(cHealthCheck.HttpHealthChecks, albsdk.HttpHealthChecks{})
	dHTTPHealth := ptr.Deref(dHealthCheck.HttpHealthChecks, albsdk.HttpHealthChecks{})
	if ptr.Deref(cHTTPHealth.Path, "") != ptr.Deref(dHTTPHealth.Path, "") ||
		!slices.Equal(cHTTPHealth.OkStatuses, dHTTPHealth.OkStatuses) {
		return true
	}

	if tlsChanged(cHTTPHealth.Tls, dHTTPHealth.Tls) {
		return true
	}

	return false
}

func targetsChanged(c, d []albsdk.Target) bool {
	return !slices.EqualFunc(c, d, func(a, b albsdk.Target) bool {
		return ptr.Deref(a.Ip, "") == ptr.Deref(b.Ip, "") && ptr.Deref(a.DisplayName, "") == ptr.Deref(b.DisplayName, "")
	})
}

func optionsChanged(current, desired *albsdk.LoadBalancerOptions) bool {
	a := ptr.Deref(ptr.Deref(current, albsdk.LoadBalancerOptions{}).AccessControl, albsdk.LoadbalancerOptionAccessControl{})
	b := ptr.Deref(ptr.Deref(desired, albsdk.LoadBalancerOptions{}).AccessControl, albsdk.LoadbalancerOptionAccessControl{})
	// The SDK considers nil and empty slices equal. slices.Equal does the same.
	return !slices.Equal(a.AllowedSourceRanges, b.AllowedSourceRanges)
}
