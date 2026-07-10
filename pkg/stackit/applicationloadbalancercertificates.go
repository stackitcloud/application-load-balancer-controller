package stackit

import (
	"context"
	"errors"
	"fmt"

	certsdk "github.com/stackitcloud/stackit-sdk-go/services/certificates/v2api"
	"k8s.io/utils/ptr"
)

type CertificatesClient interface {
	// TODO: hard-code region and project into client to make client interaction easier.
	GetCertificate(ctx context.Context, projectID, region, name string) (*certsdk.GetCertificateResponse, error)
	DeleteCertificate(ctx context.Context, projectID, region, name string) error
	CreateCertificate(ctx context.Context, projectID, region string, certificate *certsdk.CreateCertificatePayload) (*certsdk.GetCertificateResponse, error)
	ListCertificate(ctx context.Context, projectID, region string) ([]certsdk.GetCertificateResponse, error)
}

type certClient struct {
	client *certsdk.APIClient
}

var _ CertificatesClient = (*certClient)(nil)

func NewCertClient(cl *certsdk.APIClient) (CertificatesClient, error) {
	return &certClient{client: cl}, nil
}

func (cl certClient) GetCertificate(ctx context.Context, projectID, region, name string) (*certsdk.GetCertificateResponse, error) {
	cert, err := cl.client.DefaultAPI.GetCertificate(ctx, projectID, region, name).Execute()
	if isOpenAPINotFound(err) {
		return cert, fmt.Errorf("%w: %w", ErrorNotFound, err)
	}
	return cert, err
}

func (cl certClient) DeleteCertificate(ctx context.Context, projectID, region, name string) error {
	_, err := cl.client.DefaultAPI.DeleteCertificate(ctx, projectID, region, name).Execute()
	return err
}

func (cl certClient) CreateCertificate(
	ctx context.Context, projectID, region string, certificate *certsdk.CreateCertificatePayload,
) (*certsdk.GetCertificateResponse, error) {
	cert, err := cl.client.DefaultAPI.CreateCertificate(ctx, projectID, region).CreateCertificatePayload(*certificate).Execute()
	if isOpenAPINotFound(err) {
		return cert, fmt.Errorf("%w: %w", ErrorNotFound, err)
	}
	return cert, err
}

func (cl certClient) ListCertificate(ctx context.Context, projectID, region string) ([]certsdk.GetCertificateResponse, error) {
	certs := []certsdk.GetCertificateResponse{}
	var nextPage string
	pages := 0
	for {
		req := cl.client.DefaultAPI.ListCertificates(ctx, projectID, region)
		if nextPage != "" {
			req = req.PageId(nextPage)
		}
		page, err := req.Execute()
		if err != nil {
			return nil, err
		}
		certs = append(certs, page.Items...)
		if ptr.Deref(page.NextPageId, "") == "" {
			break
		}
		nextPage = *page.NextPageId
		pages++
		if pages >= 1000 {
			return nil, errors.New("maximum number of pages reached")
		}
	}
	return certs, nil
}
