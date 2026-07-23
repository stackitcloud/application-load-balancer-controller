package spec

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("test merging of labels",
	func(labels, extraLabels, expected map[string]string) {
		got := MergeExtraLabels(labels, extraLabels)
		Expect(got).To(Equal(expected))
	},
	Entry("no duplicates",
		map[string]string{"label1": "value1", "label2": "value2"},
		map[string]string{"label3": "value3"},
		map[string]string{"label1": "value1", "label2": "value2", "label3": "value3"},
	),
	Entry("duplicates",
		map[string]string{"label1": "value1", "label2": "value2"},
		map[string]string{"label1": "otherValue1"},
		map[string]string{"label1": "value1", "label2": "value2"},
	),
	Entry("labels nil",
		nil,
		map[string]string{"label1": "value1"},
		map[string]string{"label1": "value1"},
	),
	Entry("extralabels nil",
		map[string]string{"label1": "value1"},
		nil,
		map[string]string{"label1": "value1"},
	),
	Entry("labels empty",
		map[string]string{},
		map[string]string{"label1": "value1"},
		map[string]string{"label1": "value1"},
	),
	Entry("extralabels empty",
		map[string]string{"label1": "value1"},
		map[string]string{},
		map[string]string{"label1": "value1"},
	),
)
