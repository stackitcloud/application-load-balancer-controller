package diff

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	"k8s.io/utils/ptr"
)

var _ = DescribeTable("UpdateNeeded",
	func(current *albsdk.LoadBalancer, desired *albsdk.UpdateLoadBalancerPayload, expected bool) {
		Expect(UpdateNeeded(current, desired)).To(Equal(expected))
	},
	Entry("empty",
		&albsdk.LoadBalancer{},
		&albsdk.UpdateLoadBalancerPayload{},
		false,
	),
	Entry("no changes",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{Port: ptr.To[int32](80)},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{Port: ptr.To[int32](80)},
			},
		},
		false,
	),
	Entry("port changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{Port: ptr.To[int32](80)},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{Port: ptr.To[int32](443)},
			},
		},
		true,
	),
	Entry("waf config changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{WafConfigName: new("waf-1")},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{WafConfigName: new("waf-2")},
			},
		},
		true,
	),
	Entry("path prefix changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{
					Http: &albsdk.ProtocolOptionsHTTP{
						Hosts: []albsdk.HostConfig{
							{
								Rules: []albsdk.Rule{
									{Path: &albsdk.Path{Prefix: new("/api")}},
								},
							},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{
					Http: &albsdk.ProtocolOptionsHTTP{
						Hosts: []albsdk.HostConfig{
							{
								Rules: []albsdk.Rule{
									{Path: &albsdk.Path{Prefix: new("/v2")}},
								},
							},
						},
					},
				},
			},
		},
		true,
	),
	Entry("path exact match changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{
					Http: &albsdk.ProtocolOptionsHTTP{
						Hosts: []albsdk.HostConfig{
							{
								Rules: []albsdk.Rule{
									{Path: &albsdk.Path{ExactMatch: new("/api")}},
								},
							},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{
					Http: &albsdk.ProtocolOptionsHTTP{
						Hosts: []albsdk.HostConfig{
							{
								Rules: []albsdk.Rule{
									{Path: &albsdk.Path{ExactMatch: new("/v2")}},
								},
							},
						},
					},
				},
			},
		},
		true,
	),
	Entry("websocket changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{
					Http: &albsdk.ProtocolOptionsHTTP{
						Hosts: []albsdk.HostConfig{
							{
								Rules: []albsdk.Rule{
									{WebSocket: new(false)},
								},
							},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{
					Http: &albsdk.ProtocolOptionsHTTP{
						Hosts: []albsdk.HostConfig{
							{
								Rules: []albsdk.Rule{
									{WebSocket: new(true)},
								},
							},
						},
					},
				},
			},
		},
		true,
	),
	Entry("https certificates changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{
					Https: &albsdk.ProtocolOptionsHTTPS{
						CertificateConfig: &albsdk.CertificateConfig{
							CertificateIds: []string{"cert1"},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{
					Https: &albsdk.ProtocolOptionsHTTPS{
						CertificateConfig: &albsdk.CertificateConfig{
							CertificateIds: []string{"cert1", "cert2"},
						},
					},
				},
			},
		},
		true,
	),
	Entry("target pool port changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{TargetPort: ptr.To[int32](80)},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{TargetPort: ptr.To[int32](443)},
			},
		},
		true,
	),
	Entry("target pool tls validation changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						SkipCertificateValidation: new(false),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						SkipCertificateValidation: new(true),
					},
				},
			},
		},
		true,
	),
	Entry("ACL added",
		&albsdk.LoadBalancer{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: nil,
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: &albsdk.LoadbalancerOptionAccessControl{AllowedSourceRanges: []string{"1.2.3.4/32"}},
			},
		},
		true,
	),
	Entry("ACL removed",
		&albsdk.LoadBalancer{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: &albsdk.LoadbalancerOptionAccessControl{AllowedSourceRanges: []string{"1.2.3.4/32"}},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: nil,
			},
		},
		true,
	),
	Entry("ACL changed",
		&albsdk.LoadBalancer{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: &albsdk.LoadbalancerOptionAccessControl{AllowedSourceRanges: []string{"1.2.3.4/32"}},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: &albsdk.LoadbalancerOptionAccessControl{AllowedSourceRanges: []string{"2.3.4.5/32"}},
			},
		},
		true,
	),
	Entry("ACL unchanged",
		&albsdk.LoadBalancer{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: &albsdk.LoadbalancerOptionAccessControl{AllowedSourceRanges: []string{"1.2.3.4/32"}},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: &albsdk.LoadbalancerOptionAccessControl{AllowedSourceRanges: []string{"1.2.3.4/32"}},
			},
		},
		false,
	),
	Entry("ACL none",
		&albsdk.LoadBalancer{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: nil,
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Options: &albsdk.LoadBalancerOptions{
				AccessControl: nil,
			},
		},
		false,
	),
	Entry("service plan changed",
		&albsdk.LoadBalancer{
			PlanId: new("p10"),
		},
		&albsdk.UpdateLoadBalancerPayload{
			PlanId: new("p50"),
		},
		true,
	),
	Entry("label value changed",
		&albsdk.LoadBalancer{
			Labels: &map[string]string{
				"my-label": "a",
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Labels: &map[string]string{
				"my-label": "b",
			},
		},
		true,
	),
	Entry("label key changed",
		&albsdk.LoadBalancer{
			Labels: &map[string]string{
				"my-label": "a",
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Labels: &map[string]string{
				"other-label": "a",
			},
		},
		true,
	),
	Entry("labels empty vs nil no change",
		&albsdk.LoadBalancer{
			Labels: &map[string]string{},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Labels: nil,
		},
		false,
	),
	Entry("certificate changed",
		&albsdk.LoadBalancer{
			Listeners: []albsdk.Listener{
				{
					Https: &albsdk.ProtocolOptionsHTTPS{
						CertificateConfig: &albsdk.CertificateConfig{
							CertificateIds: []string{"cert-1"},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			Listeners: []albsdk.Listener{
				{
					Https: &albsdk.ProtocolOptionsHTTPS{
						CertificateConfig: &albsdk.CertificateConfig{
							CertificateIds: []string{"cert-2"},
						},
					},
				},
			},
		},
		true,
	),
	Entry("target IP changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					Targets: []albsdk.Target{
						{
							DisplayName: new("target-1"),
							Ip:          new("10.0.0.1"),
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					Targets: []albsdk.Target{
						{
							DisplayName: new("target-1"),
							Ip:          new("10.0.0.2"),
						},
					},
				},
			},
		},
		true,
	),
	Entry("target display name changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					Targets: []albsdk.Target{
						{
							DisplayName: new("target-1"),
							Ip:          new("10.0.0.1"),
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					Targets: []albsdk.Target{
						{
							DisplayName: new("target-2"),
							Ip:          new("10.0.0.1"),
						},
					},
				},
			},
		},
		true,
	),
	Entry("TLS to target explicitly disabled vs unspecified",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						Enabled: new(false),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: nil,
				},
			},
		},
		false,
	),
	Entry("TLS to target enabled",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						Enabled: new(false),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						Enabled: new(true),
					},
				},
			},
		},
		true,
	),
	Entry("TLS to target CA changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						CustomCa: new("a"),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						CustomCa: new("b"),
					},
				},
			},
		},
		true,
	),
	Entry("TLS to target skip verification enabled",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						SkipCertificateValidation: new(true),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					TlsConfig: &albsdk.TlsConfig{
						SkipCertificateValidation: new(false),
					},
				},
			},
		},
		true,
	),
	Entry("Health check alt port changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						AltPort: new(int32(8080)),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						AltPort: new(int32(8090)),
					},
				},
			},
		},
		true,
	),
	Entry("Health check healthy threshold changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HealthyThreshold: new(int32(1)),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HealthyThreshold: new(int32(2)),
					},
				},
			},
		},
		true,
	),
	Entry("Health check interval changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						Interval: new("1s"),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						Interval: new("2s"),
					},
				},
			},
		},
		true,
	),
	Entry("Health check interval jitter changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						IntervalJitter: new("1s"),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						IntervalJitter: new("2s"),
					},
				},
			},
		},
		true,
	),
	Entry("Health check timeout changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						Timeout: new("1s"),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						Timeout: new("2s"),
					},
				},
			},
		},
		true,
	),
	Entry("Health check unhealthy threshold changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						UnhealthyThreshold: new(int32(3)),
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						UnhealthyThreshold: new(int32(4)),
					},
				},
			},
		},
		true,
	),
	Entry("HTTP health check path changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Path: new("/"),
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Path: new("/health"),
						},
					},
				},
			},
		},
		true,
	),
	Entry("HTTP health check status code changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							OkStatuses: []string{"200"},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							OkStatuses: []string{"204"},
						},
					},
				},
			},
		},
		true,
	),
	Entry("HTTP health check TLS enabled",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Tls: &albsdk.TlsConfig{
								Enabled: new(false),
							},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Tls: &albsdk.TlsConfig{
								Enabled: new(true),
							},
						},
					},
				},
			},
		},
		true,
	),
	Entry("HTTP health check TLS CA changed",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Tls: &albsdk.TlsConfig{
								CustomCa: new("a"),
							},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Tls: &albsdk.TlsConfig{
								CustomCa: new("b"),
							},
						},
					},
				},
			},
		},
		true,
	),
	Entry("HTTP health check TLS skip verification enabled",
		&albsdk.LoadBalancer{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Tls: &albsdk.TlsConfig{
								SkipCertificateValidation: new(true),
							},
						},
					},
				},
			},
		},
		&albsdk.UpdateLoadBalancerPayload{
			TargetPools: []albsdk.TargetPool{
				{
					ActiveHealthCheck: &albsdk.ActiveHealthCheck{
						HttpHealthChecks: &albsdk.HttpHealthChecks{
							Tls: &albsdk.TlsConfig{
								SkipCertificateValidation: new(false),
							},
						},
					},
				},
			},
		},
		true,
	),
)
