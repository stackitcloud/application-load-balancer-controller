package config

import (
	"errors"
	"io"
	"os"

	"gopkg.in/yaml.v3"
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
	NetworkID string `yaml:"networkId"`
}

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

	if config.Global.ProjectID == "" {
		return ALBConfig{}, errors.New("project ID must be set")
	}
	if config.Global.Region == "" {
		return ALBConfig{}, errors.New("region must be set")
	}
	if config.ApplicationLoadBalancer.NetworkID == "" {
		return ALBConfig{}, errors.New("network ID must be set")
	}
	return config, nil
}
