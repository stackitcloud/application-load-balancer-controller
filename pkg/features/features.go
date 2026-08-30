package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// Every feature gate should add method here following this template:
	//
	// // MyFeature enable Foo.
	// // owner: @username
	// MyFeature featuregate.Feature = "MyFeature"

	// PodIPNetworkMode allows to use Pod IPs for ALB targets
	// owner: @hown3d
	PodIPNetworkMode featuregate.Feature = "PodIPNetworkMode"
)

var Gate = featuregate.NewFeatureGate()

// AllFeatureGates is the list of all feature gates.
var AllFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	PodIPNetworkMode: {Default: false, PreRelease: featuregate.Alpha},
}

func init() {
	runtime.Must(Gate.Add(AllFeatureGates))
}
