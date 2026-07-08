package stackit

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
)

type ApplicationLoadBalancerClient interface {
	GetLoadBalancer(ctx context.Context, projectID, region, name string) (*albsdk.LoadBalancer, error)
	DeleteLoadBalancer(ctx context.Context, projectID, region, name string) error
	CreateLoadBalancer(ctx context.Context, projectID, region string, albsdk *albsdk.CreateLoadBalancerPayload) (*albsdk.LoadBalancer, error)
	UpdateLoadBalancer(ctx context.Context, projectID, region, name string, update *albsdk.UpdateLoadBalancerPayload) (*albsdk.LoadBalancer, error)
}

type applicationLoadBalancerClient struct {
	client *albsdk.APIClient
}

var _ ApplicationLoadBalancerClient = (*applicationLoadBalancerClient)(nil)

func NewApplicationLoadBalancerClient(cl *albsdk.APIClient) (ApplicationLoadBalancerClient, error) {
	return &applicationLoadBalancerClient{client: cl}, nil
}

func (cl applicationLoadBalancerClient) GetLoadBalancer(ctx context.Context, projectID, region, name string) (*albsdk.LoadBalancer, error) {
	lb, err := cl.client.DefaultAPI.GetLoadBalancer(ctx, projectID, region, name).Execute()
	if isOpenAPINotFound(err) {
		return lb, fmt.Errorf("%w: %w", ErrorNotFound, err)
	}
	return lb, err
}

// DeleteLoadBalancer returns no error if the load balancer doesn't exist.
func (cl applicationLoadBalancerClient) DeleteLoadBalancer(ctx context.Context, projectID, region, name string) error {
	_, err := cl.client.DefaultAPI.DeleteLoadBalancer(ctx, projectID, region, name).Execute()
	return err
}

// CreateLoadBalancer returns ErrorNotFound if the project is not enabled.
func (cl applicationLoadBalancerClient) CreateLoadBalancer(
	ctx context.Context, projectID, region string, create *albsdk.CreateLoadBalancerPayload,
) (*albsdk.LoadBalancer, error) {
	lb, err := cl.client.DefaultAPI.CreateLoadBalancer(ctx, projectID, region).CreateLoadBalancerPayload(*create).XRequestID(uuid.NewString()).Execute()
	if isOpenAPINotFound(err) {
		return lb, fmt.Errorf("%w: %w", ErrorNotFound, err)
	}
	return lb, err
}

func (cl applicationLoadBalancerClient) UpdateLoadBalancer(ctx context.Context, projectID, region, name string, update *albsdk.UpdateLoadBalancerPayload) (
	*albsdk.LoadBalancer, error,
) {
	return cl.client.DefaultAPI.UpdateLoadBalancer(ctx, projectID, region, name).UpdateLoadBalancerPayload(*update).Execute()
}
