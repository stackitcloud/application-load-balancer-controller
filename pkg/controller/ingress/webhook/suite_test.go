package webhook_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

// testScheme is the scheme used by all webhook tests.
// It includes the built-in client-go scheme plus networking/v1 types.
var testScheme *runtime.Scheme

func TestWebhook(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Suite")
}

var _ = BeforeSuite(func() {
	testScheme = scheme.Scheme
	Expect(networkingv1.AddToScheme(testScheme)).To(Succeed())
})
