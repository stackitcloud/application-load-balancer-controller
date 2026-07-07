package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/stackit"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	"k8s.io/utils/ptr"
)

// ALB is an in-memory implementation of stackit.ApplicationLoadBalancerClient.
// The version of the load balancers use the following sequence: "0", "0+1", "0+1+1", "0+1+1+1", ...
type ALB struct {
	mu            sync.Mutex
	loadBalancers map[albResourceKey]*albsdk.LoadBalancer
	calls         []ALBCall

	// ExternalAddress is assigned to newly created load balancers when their
	// Options.PrivateNetworkOnly is not set to true.
	// Must not be set while the API is being called.
	ExternalAddress string
	// PrivateAddress is assigned to newly created load balancers when their
	// Options.PrivateNetworkOnly is true.
	// Must not be set while the API is being called.
	PrivateAddress string
}

// NewALB returns an ALB fake seeded with reasonable defaults for the addresses
// assigned to created load balancers.
func NewALB() *ALB {
	return &ALB{
		loadBalancers:   map[albResourceKey]*albsdk.LoadBalancer{},
		ExternalAddress: "1.2.3.4",
		PrivateAddress:  "10.0.0.1",
	}
}

var _ stackit.ApplicationLoadBalancerClient = (*ALB)(nil)

// ALBCall records a single invocation on the fake. Args holds the non-context
// arguments in declaration order.
type ALBCall struct {
	Method string
	Args   []any
}

type albResourceKey struct {
	ProjectID string
	Region    string
	Name      string
}

// LoadBalancer returns the current state of a load balancer or nil if it does
// not exist. The returned value is a deep-ish copy: the top level struct is
// copied, but nested slices/pointers are shared. Do not mutate.
func (a *ALB) LoadBalancer(projectID, region, name string) *albsdk.LoadBalancer {
	a.mu.Lock()
	defer a.mu.Unlock()
	lb, ok := a.loadBalancers[albResourceKey{projectID, region, name}]
	if !ok {
		return nil
	}
	cp := *lb
	return &cp
}

// LoadBalancers returns a snapshot of all load balancers stored in the fake.
func (a *ALB) LoadBalancers() []*albsdk.LoadBalancer {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*albsdk.LoadBalancer, 0, len(a.loadBalancers))
	for _, lb := range a.loadBalancers {
		cp := *lb
		out = append(out, &cp)
	}
	return out
}

// Calls returns a snapshot of every call that has been made to the fake, in
// order.
func (a *ALB) Calls() []ALBCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ALBCall, len(a.calls))
	copy(out, a.calls)
	return out
}

// CallsOf returns the calls matching the given method name.
func (a *ALB) CallsOf(method string) []ALBCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []ALBCall
	for _, c := range a.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (a *ALB) GetLoadBalancer(_ context.Context, projectID, region, name string) (*albsdk.LoadBalancer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record("GetLoadBalancer", projectID, region, name)
	lb, ok := a.loadBalancers[albResourceKey{projectID, region, name}]
	if !ok {
		return nil, fmt.Errorf("%w: load balancer %q", stackit.ErrorNotFound, name)
	}
	cp := *lb
	return &cp, nil
}

func (a *ALB) DeleteLoadBalancer(_ context.Context, projectID, region, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record("DeleteLoadBalancer", projectID, region, name)
	// The real API returns 200 even when the LB does not exist, so we mirror
	// that here.
	delete(a.loadBalancers, albResourceKey{projectID, region, name})
	return nil
}

func (a *ALB) CreateLoadBalancer(
	_ context.Context, projectID, region string, payload *albsdk.CreateLoadBalancerPayload,
) (*albsdk.LoadBalancer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record("CreateLoadBalancer", projectID, region, payload)
	if payload == nil || payload.Name == nil {
		return nil, fmt.Errorf("payload must set Name")
	}
	if ptr.Deref(payload.Version, "") != "" {
		return nil, fmt.Errorf("version must not be set during create")
	}
	key := albResourceKey{projectID, region, *payload.Name}
	if _, exists := a.loadBalancers[key]; exists {
		return nil, fmt.Errorf("load balancer %q already exists", *payload.Name)
	}
	lb := albsdk.LoadBalancer(*payload)
	a.materialize(&lb)
	a.loadBalancers[key] = &lb
	cp := lb
	return &cp, nil
}

func (a *ALB) UpdateLoadBalancer(
	_ context.Context, projectID, region, name string, payload *albsdk.UpdateLoadBalancerPayload,
) (*albsdk.LoadBalancer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record("UpdateLoadBalancer", projectID, region, name, payload)
	key := albResourceKey{projectID, region, name}
	existing, ok := a.loadBalancers[key]
	if !ok {
		return nil, fmt.Errorf("%w: cannot update non-existent load balancer %q", stackit.ErrorNotFound, name)
	}
	if payload == nil {
		return nil, fmt.Errorf("payload must not be nil")
	}
	if ptr.Deref(payload.Version, "") == "" {
		return nil, fmt.Errorf("version must not be nil during update")
	}
	if payload.Version != nil && existing.Version != nil && *payload.Version != *existing.Version {
		return nil, fmt.Errorf("version conflict: current %q, got %q", *existing.Version, *payload.Version)
	}
	lb := albsdk.LoadBalancer(*payload)
	a.materialize(&lb)
	a.loadBalancers[key] = &lb
	cp := lb
	return &cp, nil
}

func (a *ALB) UpdateTargetPool(
	_ context.Context, _, _, _, _ string, _ albsdk.UpdateTargetPoolPayload,
) error {
	panic("not implemented")
}

// materialize populates fields that the real API assigns server-side (Version,
// Status, external/private address).
func (a *ALB) materialize(lb *albsdk.LoadBalancer) {
	if ptr.Deref(lb.Version, "") == "" {
		lb.Version = new("0")
	} else {
		lb.Version = new(*lb.Version + "+1")
	}
	status := stackit.LBStatusReady
	lb.Status = &status
	if lb.Options != nil && lb.Options.PrivateNetworkOnly != nil && *lb.Options.PrivateNetworkOnly {
		if lb.PrivateAddress == nil {
			addr := a.PrivateAddress
			lb.PrivateAddress = &addr
		}
	} else if lb.ExternalAddress == nil {
		addr := a.ExternalAddress
		lb.ExternalAddress = &addr
	}
}

func (a *ALB) record(method string, args ...any) {
	a.calls = append(a.calls, ALBCall{Method: method, Args: args})
}

func (a *ALB) CreateCredentials(
	_ context.Context, _, _ string, _ albsdk.CreateCredentialsPayload,
) (*albsdk.CreateCredentialsResponse, error) {
	panic("not implemented")
}

func (a *ALB) ListCredentials(_ context.Context, _, _ string) (*albsdk.ListCredentialsResponse, error) {
	panic("not implemented")
}

func (a *ALB) GetCredentials(_ context.Context, _, _, _ string) (*albsdk.GetCredentialsResponse, error) {
	panic("not implemented")
}

func (a *ALB) UpdateCredentials(
	_ context.Context, _, _, _ string, _ albsdk.UpdateCredentialsPayload,
) error {
	panic("not implemented")
}

func (a *ALB) DeleteCredentials(_ context.Context, _, _, _ string) error {
	panic("not implemented")
}
