package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/stackit"
	certsdk "github.com/stackitcloud/stackit-sdk-go/services/certificates/v2api"
)

// Certs is an in-memory implementation of stackit.CertificatesClient.
type Certs struct {
	mu    sync.Mutex
	certs map[certKey]*certsdk.GetCertificateResponse
	calls []CertCall
	// counter is used to provide a unique id to every certificate.
	counter int

	// Fingerprint computes the SHA256 fingerprint that the fake stores on
	// created certificates. Tests can override this to use the same fingerprint
	// function that the code under test uses so that the controller's local
	// fingerprint matches what "the API" reports. If nil, a fingerprint of the
	// public key is used.
	Fingerprint func(publicKey, privateKey []byte) (string, error)
}

// NewCerts returns a Certs fake with a default fingerprint function that
// hashes the public key bytes.
func NewCerts() *Certs {
	return &Certs{
		certs: map[certKey]*certsdk.GetCertificateResponse{},
		Fingerprint: func(publicKey, _ []byte) (string, error) {
			sum := sha256.Sum256(publicKey)
			return hex.EncodeToString(sum[:]), nil
		},
	}
}

var _ stackit.CertificatesClient = (*Certs)(nil)

// CertCall records a single invocation on the fake.
type CertCall struct {
	Method string
	Args   []any
}

type certKey struct {
	ProjectID string
	Region    string
	ID        string
}

// Certificate returns the certificate with the given id or nil if it does not
// exist.
func (c *Certs) Certificate(projectID, region, id string) *certsdk.GetCertificateResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	got, ok := c.certs[certKey{projectID, region, id}]
	if !ok {
		return nil
	}
	cp := *got
	return &cp
}

// Certificates returns a snapshot of all certificates stored in the fake.
func (c *Certs) Certificates() []*certsdk.GetCertificateResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*certsdk.GetCertificateResponse, 0, len(c.certs))
	for _, v := range c.certs {
		cp := *v
		out = append(out, &cp)
	}
	return out
}

// Calls returns a snapshot of every call made against the fake, in order.
func (c *Certs) Calls() []CertCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CertCall, len(c.calls))
	copy(out, c.calls)
	return out
}

// CallsOf returns the calls matching the given method name.
func (c *Certs) CallsOf(method string) []CertCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []CertCall
	for _, call := range c.calls {
		if call.Method == method {
			out = append(out, call)
		}
	}
	return out
}

func (c *Certs) GetCertificate(_ context.Context, projectID, region, id string) (*certsdk.GetCertificateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record("GetCertificate", projectID, region, id)
	got, ok := c.certs[certKey{projectID, region, id}]
	if !ok {
		return nil, fmt.Errorf("%w: certificate %q", stackit.ErrorNotFound, id)
	}
	cp := *got
	return &cp, nil
}

func (c *Certs) DeleteCertificate(_ context.Context, projectID, region, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record("DeleteCertificate", projectID, region, id)
	delete(c.certs, certKey{projectID, region, id})
	return nil
}

func (c *Certs) CreateCertificate(
	_ context.Context, projectID, region string, payload *certsdk.CreateCertificatePayload,
) (*certsdk.GetCertificateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record("CreateCertificate", projectID, region, payload)
	if payload == nil {
		return nil, fmt.Errorf("payload must not be nil")
	}

	var publicKey, privateKey []byte
	if payload.PublicKey != nil {
		publicKey = []byte(*payload.PublicKey)
	}
	if payload.PrivateKey != nil {
		privateKey = []byte(*payload.PrivateKey)
	}
	fingerprint, err := c.Fingerprint(publicKey, privateKey)
	if err != nil {
		return nil, err
	}

	c.counter++
	id := fmt.Sprintf("cert-%d", c.counter)
	resp := &certsdk.GetCertificateResponse{
		Id:        &id,
		Name:      payload.Name,
		Labels:    payload.Labels,
		PublicKey: payload.PublicKey,
		Data:      &certsdk.Data{FingerprintSha256: &fingerprint},
	}
	c.certs[certKey{projectID, region, id}] = resp
	cp := *resp
	return &cp, nil
}

func (c *Certs) ListCertificate(_ context.Context, projectID, region string) (*certsdk.ListCertificatesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record("ListCertificate", projectID, region)
	var items []certsdk.GetCertificateResponse
	for k, v := range c.certs {
		if k.ProjectID != projectID || k.Region != region {
			continue
		}
		items = append(items, *v)
	}
	return &certsdk.ListCertificatesResponse{Items: items}, nil
}

func (c *Certs) record(method string, args ...any) {
	c.calls = append(c.calls, CertCall{Method: method, Args: args})
}
