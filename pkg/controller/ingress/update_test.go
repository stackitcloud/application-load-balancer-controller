package ingress

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	"k8s.io/utils/ptr"
)

var _ = DescribeTable("updateNeeded",
	func(current *albsdk.LoadBalancer, desired *albsdk.UpdateLoadBalancerPayload, expected bool) {
		Expect(updateNeeded(current, desired)).To(Equal(expected))
	},
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
)
