package spec

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = DescribeTable("computes the load balancer name",
	func(uid types.UID, className string, expected string) {
		ic := &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: className,
				UID:  uid,
			},
		}
		got := LoadBalancerName(ic)
		Expect(got).To(Equal(expected))
		Expect(len(got)).To(BeNumerically("<=", 63))
	},
	Entry("short name",
		types.UID("abc12345-6789-4def-9abc-1234567890ab"),
		"nginx",
		"k8s-ingress-abc12345-6789-4def-9abc-1234567890ab-nginx",
	),
	Entry("name exactly fills remaining space",
		types.UID("abc12345-6789-4def-9abc-1234567890ab"),
		"fifteencharsxx", // 14 chars
		"k8s-ingress-abc12345-6789-4def-9abc-1234567890ab-fifteencharsxx",
	),
	Entry("name longer than remaining space gets truncated",
		types.UID("abc12345-6789-4def-9abc-1234567890ab"),
		// 14 chars kept: "this-name-is-w"
		"this-name-is-way-too-long-to-fit-entirely",
		"k8s-ingress-abc12345-6789-4def-9abc-1234567890ab-this-name-is-w",
	),
	Entry("truncation produces a trailing dash which is trimmed",
		types.UID("abc12345-6789-4def-9abc-1234567890ab"),
		// 14th character would be a dash: "abcdefghijklmn-extra"
		"abcdefghijklm-extra",
		"k8s-ingress-abc12345-6789-4def-9abc-1234567890ab-abcdefghijklm",
	),
	Entry("truncation produces multiple trailing dashes which are all trimmed",
		types.UID("abc12345-6789-4def-9abc-1234567890ab"),
		"abcdefghijkl---extra",
		"k8s-ingress-abc12345-6789-4def-9abc-1234567890ab-abcdefghijkl",
	),
)
