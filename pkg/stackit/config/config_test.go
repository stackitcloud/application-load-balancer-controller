package config

import (
	"github.com/google/uuid"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var _ = DescribeTable("validate",
	func(mutate func(*ALBConfig), errorMatcher types.GomegaMatcher) {
		config := ALBConfig{
			Global: GlobalOpts{
				ProjectID: uuid.NewString(),
				Region:    "eu01",
			},
			ApplicationLoadBalancer: ApplicationLoadBalancerOpts{
				NetworkID: uuid.NewString(),
			},
		}
		if mutate != nil {
			mutate(&config)
		}
		Expect(validate(&config)).To(errorMatcher)
	},

	Entry("minimal configuration",
		nil,
		HaveLen(0),
	),
	Entry("minimal configuration with labels",
		func(c *ALBConfig) {
			c.ApplicationLoadBalancer.ExtraLabels = map[string]string{
				"foo": "bar",
			}
		},
		HaveLen(0),
	),
	Entry("empty",
		func(c *ALBConfig) {
			*c = ALBConfig{}
		},
		ConsistOf(
			PointTo(MatchFields(IgnoreExtras, Fields{
				"Type":  Equal(field.ErrorTypeRequired),
				"Field": Equal("global.projectId"),
			})),
			PointTo(MatchFields(IgnoreExtras, Fields{
				"Type":  Equal(field.ErrorTypeRequired),
				"Field": Equal("global.region"),
			})),
			PointTo(MatchFields(IgnoreExtras, Fields{
				"Type":  Equal(field.ErrorTypeRequired),
				"Field": Equal("applicationLoadBalancer.networkId"),
			})),
		),
	),
	Entry("invalid label",
		func(c *ALBConfig) {
			c.ApplicationLoadBalancer.ExtraLabels = map[string]string{
				"_foobar": "bar",
			}
		},
		ConsistOf(
			PointTo(MatchFields(IgnoreExtras, Fields{
				"Type":     Equal(field.ErrorTypeInvalid),
				"Field":    Equal("applicationLoadBalancer.extraLabels"),
				"BadValue": Equal("_foobar"),
			})),
		),
	),
)
