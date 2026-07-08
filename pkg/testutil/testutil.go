package testutil

import (
	"context"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func DeleteAndWaitForKubernetesResource(ctx context.Context, cl client.Client, obj client.Object) {
	GinkgoHelper()
	Expect(cl.Delete(ctx, obj)).To(Succeed())
	Eventually(func(g Gomega, ctx context.Context) {
		g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(obj), obj)).Should(
			WithTransform(apierrors.IsNotFound, BeTrue()),
			"Expected resource %s to eventually be deleted", client.ObjectKeyFromObject(obj))
	}).WithContext(ctx).Should(Succeed())
}

func HaveAtomicValue[T any](matcher types.GomegaMatcher) types.GomegaMatcher {
	return WithTransform(func(a *atomic.Pointer[T]) *T {
		t := a.Load()
		return t
	}, matcher)
}

// HaveName expects a Kubernetes resource to have the given name.
func HaveName(name string) types.GomegaMatcher {
	return WithTransform(func(o client.Object) string {
		return o.GetName()
	}, Equal(name))
}

// CreateKubernetesResourceAndDeferDeletion creates obj via cl and registers a callback to clean up some object again.
// The clean up waits until the object is gone from the API, i.e. are finalizer must be removed.
func CreateKubernetesResourceAndDeferDeletion(ctx context.Context, cl client.Client, obj client.Object) {
	GinkgoHelper()
	Expect(cl.Create(ctx, obj)).To(Succeed())
	DeferCleanup(func(ctx context.Context) {
		DeleteAndWaitForKubernetesResource(ctx, cl, obj)
	})
}

// KubernetesResource is a helper that should be used with Eventually() like this: Eventually(ctx, KubernetesResource(client, obj)).Should(HaveDeletionTimestamp()).
// It retrieves and returns obj to make assertions on it.
func KubernetesResource(cl client.Client, obj client.Object) func(ctx context.Context) (client.Object, error) {
	key := client.ObjectKeyFromObject(obj)
	return func(ctx context.Context) (client.Object, error) {
		if err := cl.Get(ctx, key, obj); err != nil {
			return nil, err
		}
		return obj, nil
	}
}
