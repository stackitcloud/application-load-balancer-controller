// revive:disable:exported // This file will be dot-imported.

package service

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Service(namespace, name string, opts ...ServiceOption) corev1.Service {
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{},
		},
	}
	for _, o := range opts {
		o(&service)
	}
	return service
}

type ServiceOption func(service *corev1.Service)

func WithPort(name string, port, nodePort int32, protocol corev1.Protocol) ServiceOption {
	return func(service *corev1.Service) {
		service.Spec.Ports = append(service.Spec.Ports, corev1.ServicePort{
			Name:     name,
			Port:     port,
			NodePort: nodePort,
			Protocol: protocol,
		})
	}
}

func WithServiceType(_type corev1.ServiceType) ServiceOption {
	return func(service *corev1.Service) {
		service.Spec.Type = _type
	}
}

func WithServiceAnnotation(key, value string) ServiceOption {
	return func(service *corev1.Service) {
		service.Annotations[key] = value
	}
}
