package ingress

import (
	"testing"

	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	"k8s.io/utils/ptr"
)

func Test_updateNeeded(t *testing.T) {
	tests := []struct {
		name     string
		current  *albsdk.LoadBalancer
		desired  *albsdk.UpdateLoadBalancerPayload
		expected bool
	}{
		{
			name: "no changes",
			current: &albsdk.LoadBalancer{
				Listeners: []albsdk.Listener{
					{Port: ptr.To[int32](80)},
				},
			},
			desired: &albsdk.UpdateLoadBalancerPayload{
				Listeners: []albsdk.Listener{
					{Port: ptr.To[int32](80)},
				},
			},
			expected: false,
		},
		{
			name: "port changed",
			current: &albsdk.LoadBalancer{
				Listeners: []albsdk.Listener{
					{Port: ptr.To[int32](80)},
				},
			},
			desired: &albsdk.UpdateLoadBalancerPayload{
				Listeners: []albsdk.Listener{
					{Port: ptr.To[int32](443)},
				},
			},
			expected: true,
		},
		{
			name: "waf config changed",
			current: &albsdk.LoadBalancer{
				Listeners: []albsdk.Listener{
					{WafConfigName: new("waf-1")},
				},
			},
			desired: &albsdk.UpdateLoadBalancerPayload{
				Listeners: []albsdk.Listener{
					{WafConfigName: new("waf-2")},
				},
			},
			expected: true,
		},
		{
			name: "path prefix changed",
			current: &albsdk.LoadBalancer{
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
			desired: &albsdk.UpdateLoadBalancerPayload{
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
			expected: true,
		},
		{
			name: "path exact match changed",
			current: &albsdk.LoadBalancer{
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
			desired: &albsdk.UpdateLoadBalancerPayload{
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
			expected: true,
		},
		{
			name: "websocket changed",
			current: &albsdk.LoadBalancer{
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
			desired: &albsdk.UpdateLoadBalancerPayload{
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
			expected: true,
		},
		{
			name: "https certificates changed",
			current: &albsdk.LoadBalancer{
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
			desired: &albsdk.UpdateLoadBalancerPayload{
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
			expected: true,
		},
		{
			name: "target pool port changed",
			current: &albsdk.LoadBalancer{
				TargetPools: []albsdk.TargetPool{
					{TargetPort: ptr.To[int32](80)},
				},
			},
			desired: &albsdk.UpdateLoadBalancerPayload{
				TargetPools: []albsdk.TargetPool{
					{TargetPort: ptr.To[int32](443)},
				},
			},
			expected: true,
		},
		{
			name: "target pool tls validation changed",
			current: &albsdk.LoadBalancer{
				TargetPools: []albsdk.TargetPool{
					{
						TlsConfig: &albsdk.TlsConfig{
							SkipCertificateValidation: new(false),
						},
					},
				},
			},
			desired: &albsdk.UpdateLoadBalancerPayload{
				TargetPools: []albsdk.TargetPool{
					{
						TlsConfig: &albsdk.TlsConfig{
							SkipCertificateValidation: new(true),
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := updateNeeded(tt.current, tt.desired); got != tt.expected {
				t.Errorf("updateNeeded() = %v, want %v", got, tt.expected)
			}
		})
	}
}
