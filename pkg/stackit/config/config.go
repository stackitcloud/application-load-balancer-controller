package config

import (
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type GlobalOpts struct {
	ProjectID    string       `yaml:"projectId"`
	Region       string       `yaml:"region"`
	APIEndpoints APIEndpoints `yaml:"apiEndpoints"`
}

type APIEndpoints struct {
	ApplicationLoadBalancerAPI            string `yaml:"applicationLoadBalancerApi"`
	ApplicationLoadBalancerCertificateAPI string `yaml:"applicationLoadBalancerCertificateApi"`
}

type ALBConfig struct {
	Global                  GlobalOpts                  `yaml:"global"`
	ApplicationLoadBalancer ApplicationLoadBalancerOpts `yaml:"applicationLoadBalancer"`
}
type ApplicationLoadBalancerOpts struct {
	NetworkID   string            `yaml:"networkId"`
	ExtraLabels map[string]string `yaml:"extraLabels"`
}

var (
	keyRegex   = regexp.MustCompile(`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])$`)
	valueRegex = regexp.MustCompile(`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?$`)
)

func readFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return []byte{}, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

func ReadALBConfigFromFile(path string) (ALBConfig, error) {
	content, err := readFile(path)
	if err != nil {
		return ALBConfig{}, err
	}

	config := ALBConfig{}
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		return ALBConfig{}, err
	}

	if errs := validate(&config); len(errs) > 0 {
		return ALBConfig{}, errs.ToAggregate()
	}

	return config, nil
}

func validate(config *ALBConfig) field.ErrorList {
	allErrors := field.ErrorList{}

	allErrors = append(allErrors, validateGlobal(config.Global, field.NewPath("global"))...)
	allErrors = append(allErrors, validateApplicationLoadBalancer(config.ApplicationLoadBalancer, field.NewPath("applicationLoadBalancer"))...)

	return allErrors
}

func validateGlobal(global GlobalOpts, fld *field.Path) field.ErrorList {
	allErrors := field.ErrorList{}

	if global.ProjectID == "" {
		allErrors = append(allErrors, field.Required(fld.Child("projectId"), ""))
	}
	if global.Region == "" {
		allErrors = append(allErrors, field.Required(fld.Child("region"), ""))
	}

	return allErrors
}

func validateApplicationLoadBalancer(opts ApplicationLoadBalancerOpts, fld *field.Path) field.ErrorList {
	allErrors := field.ErrorList{}

	if opts.NetworkID == "" {
		allErrors = append(allErrors, field.Required(fld.Child("networkId"), ""))
	}
	allErrors = append(allErrors, validateExtraLabels(opts.ExtraLabels, fld.Child("extraLabels"))...)

	return allErrors
}

func validateExtraLabels(extraLabels map[string]string, fld *field.Path) field.ErrorList {
	allErrors := field.ErrorList{}
	for k, v := range extraLabels {
		if !keyRegex.MatchString(k) {
			allErrors = append(allErrors, field.Invalid(fld, k, "key does not match allowed format"))
		}
		if !valueRegex.MatchString(v) {
			allErrors = append(allErrors, field.Invalid(fld, v, "key does not match allowed format"))
		}
	}
	return allErrors
}
