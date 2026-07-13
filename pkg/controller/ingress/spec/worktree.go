package spec

import (
	"cmp"
	"crypto/sha256"
	cryptotls "crypto/tls"
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/kubeutil"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	certsdk "github.com/stackitcloud/stackit-sdk-go/services/certificates/v2api"
)

type CertificateFingerprint string

// WorkTreeALB is a temporary structure to build up an ALB specification from ingresses.
// It contains the relevant logic to merge multiple ingresses and report errors for invalid or conflicting ingresses.
//
// The zero value is invalid. Use BuildTree() to create a work tree.
//
// Look at the methods how a work tree can be used.
type WorkTreeALB struct {
	ingressClass  *networkingv1.IngressClass
	planID        string
	waf           string
	accessControl *albsdk.LoadbalancerOptionAccessControl
	internalLB    bool
	externalIP    string

	listeners map[uint16]*workTreeListener
	// We can already create the real type because there is nothing to merge or track.
	targetPools  map[ingressPathReference]*albsdk.TargetPool
	certificates map[CertificateFingerprint]WorkTreeCertificate

	existingALB *albsdk.LoadBalancer
}

type workTreeListener struct {
	hosts    map[string]*workTreeHost
	protocol albsdk.ListenerProtocol
}

type pathWithType struct {
	pathType networkingv1.PathType
	path     string
}

type workTreeHost struct {
	paths map[pathWithType]*workTreePath
}

type ingressPathReference struct {
	namespace string
	name      string
	uid       string
	ruleIndex int
	pathIndex int
}

// toTargetPoolName returns the desired target pool name for this path reference.
// It globally identifies this path via UID of the ingress.
func (i *ingressPathReference) toTargetPoolName() string {
	return fmt.Sprintf("%s-%d-%d", i.uid, i.ruleIndex, i.pathIndex)
}

type workTreePath struct {
	path                 pathWithType
	ingressPathReference ingressPathReference
	websocket            bool
}

type WorkTreeCertificate struct {
	PublicKey  string
	PrivateKey string
	// Ports tracks all HTTPS ports that use that certificate. The values of the map are not used. Only presence matters.
	Ports map[uint16]any
}

var servicePlans = []string{
	"p10",
}

// BuildTree creates a new work tree.
// It tries to fit as much ingresses into the work tree as possible, bound by the limits of the application load balancer.
//
// Every ingress rule translates into 1 or 2 rules in the ALB, depending on the protocols used for that ingress.
//
// If existingALB is nil it is assumed that no load balancer exists yet.
// existingALB is used to pick up fields that are already set, most notably the version for the update payload.
//
// The arguments must only contain data related to the ingress class.
// I.e. all ingresses will be processed regardless of their ingress class reference.
//
// This function changes the order of the slice ingresses.
//
// This function either return a tree and some error events or a nil tree and an error indicating that the entire ALB is invalid.
func BuildTree( //nolint:gocyclo,funlen // Breaking up this function won't make it much simpler.
	ingressClass *networkingv1.IngressClass,
	ingresses []networkingv1.Ingress,
	secrets []corev1.Secret,
	services []corev1.Service,
	nodes []corev1.Node,
	existingALB *albsdk.LoadBalancer,
) (*WorkTreeALB, []ErrorEvent, error) {
	errors := []ErrorEvent{}

	servicesMap := map[types.NamespacedName]corev1.Service{}
	for i := range services {
		servicesMap[client.ObjectKeyFromObject(&services[i])] = services[i]
	}
	secretsMap := map[types.NamespacedName]corev1.Secret{}
	for i := range secrets {
		secretsMap[client.ObjectKeyFromObject(&secrets[i])] = secrets[i]
	}

	targets := getTargetsOfNodes(nodes)

	externalIP, err := parseExternalIP(ingressClass)
	if err != nil {
		return nil, nil, err
	}

	tree := &WorkTreeALB{
		ingressClass: ingressClass,
		planID:       GetAnnotation(AnnotationPlanID, "", ingressClass),
		waf:          GetAnnotation(AnnotationWAFName, "", ingressClass),
		internalLB:   GetAnnotation(AnnotationInternal, false, ingressClass),
		externalIP:   externalIP,

		listeners:    map[uint16]*workTreeListener{},
		targetPools:  map[ingressPathReference]*albsdk.TargetPool{},
		existingALB:  existingALB,
		certificates: map[CertificateFingerprint]WorkTreeCertificate{},
	}

	addAccessControlToTree(tree, ingressClass)

	slices.SortFunc(ingresses, func(a, b networkingv1.Ingress) int {
		if diff := GetAnnotation(AnnotationPriority, 0, &b) - GetAnnotation(AnnotationPriority, 0, &a); diff != 0 {
			return diff
		}
		if diff := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); diff != 0 {
			return diff
		}
		return cmp.Compare(fmt.Sprintf("%s/%s", a.Namespace, a.Name),
			fmt.Sprintf("%s/%s", b.Namespace, b.Name))
	})
	for i := range ingresses {
		ingress := &ingresses[i]
		httpsOnly := GetAnnotation(AnnotationHTTPSOnly, false, ingress, ingressClass)
		httpPort := GetAnnotation(AnnotationHTTPPort, 80, ingress, ingressClass)
		httpsPort := GetAnnotation(AnnotationHTTPSPort, 443, ingress, ingressClass)

		if !httpsOnly && (httpPort <= 0 || httpPort > math.MaxUint16) {
			errors = append(errors, ErrorEvent{
				Ingress:     ingress,
				Description: "HTTP port is out of range.",
			})
			continue
		}
		if len(ingress.Spec.TLS) > 0 && (httpsPort <= 0 || httpsPort > math.MaxUint16) {
			errors = append(errors, ErrorEvent{
				Ingress:     ingress,
				Description: "HTTPS port is out of range.",
			})
			continue
		}

		for tlsIndex, tls := range ingress.Spec.TLS {
			secret, exists := secretsMap[types.NamespacedName{Namespace: ingress.Namespace, Name: tls.SecretName}]
			if !exists {
				errors = append(errors, ErrorEvent{
					Ingress:     ingress,
					FieldPath:   field.NewPath("spec", "tls").Index(tlsIndex).Child("secretName"),
					Description: "TLS secret doesn't exist",
				})
				continue
			}
			if secret.Type != corev1.SecretTypeTLS {
				errors = append(errors, ErrorEvent{
					Ingress:     ingress,
					FieldPath:   field.NewPath("spec", "tls").Index(tlsIndex).Child("secretName"),
					Description: "TLS secret isn't of type kubernetes.io/tls",
				})
				continue
			}

			fingerprint, err := ValidateTLSCertAndFingerprint(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
			if err != nil {
				errors = append(errors, ErrorEvent{
					Ingress:     ingress,
					FieldPath:   field.NewPath("spec", "tls").Index(tlsIndex).Child("secretName"),
					Description: fmt.Sprintf("invalid certificate: %s", err.Error()),
				})
				continue
			}

			if _, exists := tree.certificates[CertificateFingerprint(fingerprint)]; !exists {
				tree.certificates[CertificateFingerprint(fingerprint)] = WorkTreeCertificate{
					PublicKey:  string(secret.Data[corev1.TLSCertKey]),
					PrivateKey: string(secret.Data[corev1.TLSPrivateKeyKey]),
					Ports:      map[uint16]any{},
				}
			}
			tree.certificates[CertificateFingerprint(fingerprint)].Ports[uint16(httpsPort)] = nil //nolint:gosec // httpsPort is bounds-checked above
		}

		for ruleIndex, rule := range ingress.Spec.Rules {
			// TODO: support rules that don't have a path
			if rule.HTTP == nil {
				continue
			}
			for pathIndex, path := range rule.HTTP.Paths {
				ingressPathRef := ingressPathReference{
					namespace: ingress.Namespace, name: ingress.Name, uid: string(ingress.UID),
					ruleIndex: ruleIndex, pathIndex: pathIndex,
				}

				targetPool, e := buildTargetPool(tree, ingressClass, targets, ingress, ruleIndex, path, pathIndex, servicesMap)
				errors = append(errors, e...)
				if targetPool == nil {
					continue // If the target pool is invalid we do not add any rules.
				}

				var httpAdded, httpsAdded bool
				if !httpsOnly {
					//nolint:gosec // httpPort is bounds-checked above
					httpAdded, e = tree.addPath(ingressClass, ingress, rule, ruleIndex, path, pathIndex, uint16(httpPort), albsdk.LISTENERPROTOCOL_PROTOCOL_HTTP)
					errors = append(errors, e...)
				}
				if len(ingress.Spec.TLS) > 0 {
					//nolint:gosec // httpsPort is bounds-checked above
					httpsAdded, e = tree.addPath(ingressClass, ingress, rule, ruleIndex, path, pathIndex, uint16(httpsPort), albsdk.LISTENERPROTOCOL_PROTOCOL_HTTPS)
					errors = append(errors, e...)
				}

				// We only add the target pool if at least one rule was added that references the target pool.
				if httpAdded || httpsAdded {
					tree.targetPools[ingressPathRef] = targetPool
				}
			}
		}
	}

	return tree, errors, nil
}

func parseExternalIP(ingressClass *networkingv1.IngressClass) (string, error) {
	externalIP := GetAnnotation(AnnotationExternalIP, "", ingressClass)
	if externalIP != "" {
		addr, err := netip.ParseAddr(externalIP)
		if err != nil {
			return "", fmt.Errorf("failed to parse external IP annotation: %w", err)
		}
		if !addr.Is4() {
			return "", fmt.Errorf("external IP annotation is not an IPv4 address")
		}
	}
	return externalIP, nil
}

func addAccessControlToTree(tree *WorkTreeALB, ingressClass *networkingv1.IngressClass) {
	annotation := GetAnnotation(AnnotationAllowedSourceRanges, "", ingressClass)
	if annotation == "" {
		return
	}
	ranges := strings.Split(annotation, ",")
	tree.accessControl = &albsdk.LoadbalancerOptionAccessControl{
		AllowedSourceRanges: ranges,
	}
}

// addPath adds the given path to tree under the given port and protocol.
// It implicitly creates listeners and hosts that don't exist yet in tree.
func (t *WorkTreeALB) addPath(
	ingressClass *networkingv1.IngressClass, ingress *networkingv1.Ingress,
	rule networkingv1.IngressRule, ruleIndex int, path networkingv1.HTTPIngressPath, pathIndex int,
	port uint16, protocol albsdk.ListenerProtocol,
) (added bool, errors []ErrorEvent) {
	pathAndType := pathWithType{pathType: ptr.Deref(path.PathType, networkingv1.PathTypeExact), path: path.Path}
	ingressPathRef := ingressPathReference{namespace: ingress.Namespace, name: ingress.Name, uid: string(ingress.UID), ruleIndex: ruleIndex, pathIndex: pathIndex}

	listener, exists := t.listeners[port]
	if !exists {
		listener = &workTreeListener{
			hosts:    map[string]*workTreeHost{},
			protocol: protocol,
		}
	}
	if listener.protocol != protocol {
		// TODO: This error is redundant if the ingress contains multiple rules. Move this check "up".
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec"),
			Description: fmt.Sprintf("Listener with port %d has protocol %s but ingress uses the port for %s", port, listener.protocol, protocol),
		})
		return false, errors
	}

	host, exists := listener.hosts[rule.Host]
	if !exists {
		host = &workTreeHost{
			paths: map[pathWithType]*workTreePath{},
		}
	}

	_, exists = host.paths[pathAndType]
	if exists {
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex),
			Description: "Path already exists",
		})
		return false, errors
	}
	albPath := &workTreePath{
		path:                 pathAndType,
		ingressPathReference: ingressPathRef,
		websocket:            GetAnnotation(AnnotationWebSocket, false, ingress, ingressClass),
	}

	// We assign listener and host whether they exist or not. If they already exist we assign them to the same pointer.
	t.listeners[port] = listener
	listener.hosts[rule.Host] = host

	host.paths[pathAndType] = albPath
	return true, errors
}

const (
	kubeProxyHealthCheckEndpoint    = "/healthz"
	kubeProxyExpectedHTTPStatusCode = "200"

	healthCheckHealthyThreshold   int32 = 1
	healthCheckInterval                 = "5s"
	healthCheckIntervalJitter           = "1s"
	healthCheckTimeout                  = "3s"
	healthCheckUnhealthyThreshold int32 = 3
)

// buildTargetPool builds a target pool for the provided path.
// It uses tree to validate the returned target pool against the existing state.
//
// This function doesn't mutate tree or any other arguments.
// If the target pool is not valid nil is returned together with a list of errors.
func buildTargetPool( //nolint:gocyclo,funlen // TODO: Make function easier?!
	tree *WorkTreeALB, ingressClass *networkingv1.IngressClass, targets []albsdk.Target, ingress *networkingv1.Ingress,
	ruleIndex int, path networkingv1.HTTPIngressPath, pathIndex int, servicesMap map[types.NamespacedName]corev1.Service,
) (*albsdk.TargetPool, []ErrorEvent) {
	errors := []ErrorEvent{}

	ingressPathRef := ingressPathReference{namespace: ingress.Namespace, name: ingress.Name, uid: string(ingress.UID), ruleIndex: ruleIndex, pathIndex: pathIndex}

	_, exists := tree.targetPools[ingressPathRef]
	if !exists && len(tree.targetPools) >= LimitTargetPools {
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex),
			Description: "Target pool limit reached. Path will be ignored.",
		})
		return nil, errors
	}

	// TODO: Support other backends than services.
	if path.Backend.Service == nil {
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex).Child("backend"),
			Description: "Backend of path isn't a service.",
		})
		return nil, errors
	}
	service, exists := servicesMap[types.NamespacedName{Namespace: ingress.Namespace, Name: path.Backend.Service.Name}]
	if !exists {
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex).Child("backend", "service", "name"),
			Description: "Service doesn't exist",
		})
		return nil, errors
	}
	if service.Spec.Type != corev1.ServiceTypeNodePort && service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex).Child("backend", "service", "name"),
			Description: "Service is not of type NodePort or LoadBalancer",
		})
		return nil, errors
	}
	nodePort := int32(0)
	for _, port := range service.Spec.Ports {
		// We must not match an empty port name against an empty port name.
		if port.Port == path.Backend.Service.Port.Number ||
			(port.Name != "" && port.Name == path.Backend.Service.Port.Name) {
			if port.NodePort == 0 {
				errors = append(errors, ErrorEvent{
					Ingress:     ingress,
					FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex).Child("backend", "service"),
					Description: "Service port doesn't have a node port",
				})
				continue
			}
			nodePort = port.NodePort
		}
	}
	if nodePort == 0 {
		errors = append(errors, ErrorEvent{
			Ingress:     ingress,
			FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex).Child("backend", "service"),
			Description: "Port not found in service.",
		})
		return nil, errors
	}

	targetPool := &albsdk.TargetPool{
		Name:       new(ingressPathRef.toTargetPoolName()),
		TargetPort: new(nodePort),
		Targets:    targets,
	}
	targetPool.TlsConfig = &albsdk.TlsConfig{
		Enabled:                   new(GetAnnotation(AnnotationTargetPoolTLSEnabled, false, &service, ingress, ingressClass)),
		SkipCertificateValidation: new(GetAnnotation(AnnotationTargetPoolTLSSkipCertificateValidation, false, &service, ingress, ingressClass)),
	}
	if ca := GetAnnotation(AnnotationTargetPoolTLSCustomCa, "", &service, ingress, ingressClass); ca != "" {
		targetPool.TlsConfig.CustomCa = new(ca)
	}
	// If externalTrafficPolicy=Cluster we use the default TCP health check on the node port itself.
	if service.Spec.ExternalTrafficPolicy == corev1.ServiceExternalTrafficPolicyLocal {
		if service.Spec.HealthCheckNodePort == 0 {
			errors = append(errors, ErrorEvent{
				Ingress:     ingress,
				FieldPath:   field.NewPath("spec", "rules").Index(ruleIndex).Child("paths").Index(pathIndex).Child("backend", "service"),
				Description: "Service has externalTrafficPolicy=Local but doesn't have a health check node port. The service must be of type LoadBalancer.",
			})
			return nil, errors
		}
		targetPool.ActiveHealthCheck = &albsdk.ActiveHealthCheck{
			AltPort: &service.Spec.HealthCheckNodePort,
			HttpHealthChecks: &albsdk.HttpHealthChecks{
				Path:       new(kubeProxyHealthCheckEndpoint),
				OkStatuses: []string{kubeProxyExpectedHTTPStatusCode},
			},
			// If ActiveHealthCheck is set then all fields in it have to be set.
			// The fields below are not strictly needed for externalTrafficPolicy=Local.
			HealthyThreshold:   new(healthCheckHealthyThreshold),
			Interval:           new(healthCheckInterval),
			IntervalJitter:     new(healthCheckIntervalJitter),
			Timeout:            new(healthCheckTimeout),
			UnhealthyThreshold: new(healthCheckUnhealthyThreshold),
		}
	}

	return targetPool, errors
}

// ValidateTLSCertAndFingerprint ensures that the private and public are parseable.
// If they are parseable then the SHA256 hash of the public key is returned.
func ValidateTLSCertAndFingerprint(publicKey, privateKey []byte) (string, error) {
	cert, err := cryptotls.X509KeyPair(publicKey, privateKey)
	if err != nil {
		return "", err
	}
	sha256Hash := sha256.Sum256(cert.Leaf.Raw)
	return hex.EncodeToString(sha256Hash[:]), nil
}

// getTargetsOfNodes returns all targets that should be used for the application load balancer.
// It filters out nodes that don't qualify as targets.
// The returned slice is sorted.
func getTargetsOfNodes(nodes []corev1.Node) []albsdk.Target {
	targets := []albsdk.Target{}
	for i := range nodes {
		node := &nodes[i]
		if isNodeTerminating(node) {
			continue
		}
		for j := range node.Status.Addresses {
			address := node.Status.Addresses[j]
			if address.Type == corev1.NodeInternalIP {
				targets = append(targets, albsdk.Target{
					DisplayName: &node.Name, // TODO: Sanitize node name (see CCM)
					Ip:          &address.Address,
				})
				break
			}
		}
	}
	slices.SortFunc(targets, func(a, b albsdk.Target) int {
		return cmp.Compare(*a.Ip, *b.Ip)
	})
	return targets
}

// GetMissingCertificates returns all certificates that are required by t except those that it finds in existingCert.
// It can be used to create all remaining certificates required to create the ALB.
//
// This function uses the SHA256 fingerprint from the response to match existing certificates.
func (t *WorkTreeALB) GetMissingCertificates(existingCerts []certsdk.GetCertificateResponse) map[CertificateFingerprint]WorkTreeCertificate {
	missingCerts := map[CertificateFingerprint]WorkTreeCertificate{}
	existingCertsMap := map[CertificateFingerprint]any{}
	for _, cert := range existingCerts {
		if cert.Data == nil || cert.Data.FingerprintSha256 == nil {
			continue
		}
		existingCertsMap[CertificateFingerprint(*cert.Data.FingerprintSha256)] = nil
	}

	for fingerprint, cert := range t.certificates {
		if _, exists := existingCertsMap[fingerprint]; exists {
			continue
		}
		missingCerts[fingerprint] = cert
	}
	return missingCerts
}

// GetUnusedCertificates returns all certificates in existingCerts that are not referenced in t.
func (t *WorkTreeALB) GetUnusedCertificates(existingCerts map[CertificateFingerprint]string) map[CertificateFingerprint]string {
	unused := maps.Clone(existingCerts)
	for fingerprint := range t.certificates {
		delete(unused, fingerprint)
	}
	return unused
}

// ToCreatePayload return the payload to request the creation of the ALB in the API based on t.
//
// certificateIDMap must contain all certificates that exist in the API for this ALB.
// Certificates that are referenced in t but missing in certificateIDMap are not included in the payload.
//
// All lists in the update payload are sorted to simplify change detection.
func (t *WorkTreeALB) ToCreatePayload( //nolint:gocyclo,funlen // Breaking up this function won't make it much simpler.
	certificateIDMap map[CertificateFingerprint]string,
	networkID string,
	region string,
) *albsdk.CreateLoadBalancerPayload {
	listeners := []albsdk.Listener{}
	for port, listener := range t.listeners {
		hosts := []albsdk.HostConfig{}
		for hostname, host := range listener.hosts {
			paths := slices.Collect(maps.Values(host.paths))
			sortPaths(paths)
			rules := []albsdk.Rule{}
			for _, path := range paths {
				rule := albsdk.Rule{
					TargetPool: new(path.ingressPathReference.toTargetPoolName()),
					WebSocket:  &path.websocket,
				}

				switch path.path.pathType {
				case networkingv1.PathTypeExact, networkingv1.PathTypeImplementationSpecific:
					rule.Path = new(albsdk.Path{
						ExactMatch: new(path.path.path),
					})
				default:
					rule.Path = new(albsdk.Path{
						Prefix: new(path.path.path),
					})
				}

				rules = append(rules, rule)
			}

			hosts = append(hosts, albsdk.HostConfig{
				Host:  &hostname,
				Rules: rules,
			})
		}
		sortHosts(hosts)

		var https *albsdk.ProtocolOptionsHTTPS
		prot := albsdk.LISTENERPROTOCOL_PROTOCOL_HTTP
		if listener.protocol == albsdk.LISTENERPROTOCOL_PROTOCOL_HTTPS {
			prot = albsdk.LISTENERPROTOCOL_PROTOCOL_HTTPS
			https = &albsdk.ProtocolOptionsHTTPS{
				CertificateConfig: &albsdk.CertificateConfig{
					CertificateIds: []string{},
				},
			}
			for fingerprint, cert := range t.certificates {
				if _, intendedForPort := cert.Ports[port]; !intendedForPort {
					continue
				}
				if id, exists := certificateIDMap[fingerprint]; exists {
					https.CertificateConfig.CertificateIds = append(https.CertificateConfig.CertificateIds, id)
				}
			}
			slices.Sort(https.CertificateConfig.CertificateIds)
			if len(https.CertificateConfig.CertificateIds) == 0 {
				// The API doesn't allow an HTTPS port without certificate. So we drop the port if no certificate was provided.
				continue
			}
		}

		var waf *string
		if t.waf != "" {
			waf = new(t.waf)
		}
		listeners = append(listeners, albsdk.Listener{
			Name:          new(fmt.Sprintf("port-%d", port)),
			WafConfigName: waf,
			Protocol:      new(prot),
			Port:          new(int32(port)),
			Http: &albsdk.ProtocolOptionsHTTP{
				Hosts: hosts,
			},
			Https: https,
		})
	}
	sortListeners(listeners)

	if len(listeners) == 0 {
		// The ALB doesn't allow zero listeners. To already create it we create an empty listener on port 80.
		listeners = append(listeners, albsdk.Listener{
			Name:     new(fmt.Sprintf("dummy-port-%d", 80)),
			Protocol: new(albsdk.LISTENERPROTOCOL_PROTOCOL_HTTP),
			Port:     new(int32(80)),
			Http: &albsdk.ProtocolOptionsHTTP{
				Hosts: []albsdk.HostConfig{},
			},
		})
	}

	targetPools := []albsdk.TargetPool{}
	for _, targetPool := range t.targetPools {
		targetPools = append(targetPools, *targetPool)
	}
	sortTargetPools(targetPools)

	var externalAddress *string
	if t.externalIP != "" {
		externalAddress = new(t.externalIP)
	}
	ephemeralAddress := new(false)
	if t.externalIP == "" && !t.internalLB {
		// Counter-intuitively an internal LB must set ephemeral address to false.
		// So the only case where the values needs to be set to true is for public LBs without an existing IP.
		ephemeralAddress = new(true)
	}

	return &albsdk.CreateLoadBalancerPayload{
		DisableTargetSecurityGroupAssignment: new(true), // TODO: Make this configurable via flag.
		Name:                                 new(LoadBalancerName(t.ingressClass)),
		Labels: &map[string]string{
			"ingress-class-uid": string(t.ingressClass.UID),
		},
		Listeners: listeners,
		Networks: []albsdk.Network{
			{
				NetworkId: new(networkID),
				Role:      new(albsdk.NETWORKROLE_ROLE_LISTENERS_AND_TARGETS),
			},
		},
		ExternalAddress: externalAddress,
		Options: &albsdk.LoadBalancerOptions{
			EphemeralAddress:   ephemeralAddress,
			AccessControl:      t.accessControl,
			PrivateNetworkOnly: new(t.internalLB),
		},
		PlanId:      &t.planID,
		Region:      new(region),
		TargetPools: targetPools,
	}
}

// ToUpdatePayload creates the payload to update a load balancer from the work tree.
// It requires that existingALB was not nil when BuildTree was called.
//
// See ToCreatePayload for more details.
//
// The log configuration is taken from the existing load balancer to allow for out-of-band changes of this field.
func (t *WorkTreeALB) ToUpdatePayload(
	certificateIDMap map[CertificateFingerprint]string,
	networkID string,
	region string,
) *albsdk.UpdateLoadBalancerPayload {
	create := t.ToCreatePayload(certificateIDMap, networkID, region)
	update := new(albsdk.UpdateLoadBalancerPayload{
		DisableTargetSecurityGroupAssignment: create.DisableTargetSecurityGroupAssignment,
		ExternalAddress:                      create.ExternalAddress,
		Labels:                               create.Labels,
		Listeners:                            create.Listeners,
		Name:                                 create.Name,
		Networks:                             create.Networks,
		Options:                              create.Options,
		PlanId:                               create.PlanId,
		Region:                               create.Region,
		TargetPools:                          create.TargetPools,
	})
	if t.existingALB.Options != nil && t.existingALB.Options.Observability != nil && t.existingALB.Options.Observability.Logs != nil {
		update.Options.Observability = &albsdk.LoadbalancerOptionObservability{
			Logs: t.existingALB.Options.Observability.Logs,
		}
	}
	update.Version = t.existingALB.Version
	return update
}

const (
	// From https://github.com/kubernetes/cloud-provider/blob/81e4f58b4d1badd71d633d356faaaf69d971d874/controllers/service/controller.go#L64C2-L64C53
	TaintToBeDeleted = "ToBeDeletedByClusterAutoscaler"
	// From https://github.com/gardener/machine-controller-manager/blob/fc341881a5e71d7c5f240ca73415f967084aa85b/pkg/util/provider/machineutils/utils.go#L61
	ConditionNodeTermination corev1.NodeConditionType = "Terminating"
)

func isNodeTerminating(node *corev1.Node) bool {
	if kubeutil.GetTaint(node, TaintToBeDeleted) != nil {
		return true
	}
	if cond := kubeutil.GetNodeCondition(node, ConditionNodeTermination); cond != nil && cond.Status == corev1.ConditionTrue {
		return true
	}
	return false
}

// pathTypeRank ranks the path types in the order in which the should appear in the ALB, lowest number first.
var pathTypeRank = map[networkingv1.PathType]int{
	networkingv1.PathTypeExact:                  1,
	networkingv1.PathTypeImplementationSpecific: 2,
	networkingv1.PathTypePrefix:                 3,
}

func sortPaths(paths []*workTreePath) {
	slices.SortFunc(paths, func(a, b *workTreePath) int {
		if x := cmp.Compare(pathTypeRank[a.path.pathType], pathTypeRank[b.path.pathType]); x != 0 {
			return x
		}
		if x := cmp.Compare(len(b.path.path), len(a.path.path)); x != 0 {
			return x
		}
		return cmp.Compare(a.path.path, b.path.path)
	})
}

func sortListeners(listeners []albsdk.Listener) {
	slices.SortFunc(listeners, func(a, b albsdk.Listener) int {
		return int(*a.Port - *b.Port)
	})
}

func sortTargetPools(targetPools []albsdk.TargetPool) {
	slices.SortFunc(targetPools, func(a, b albsdk.TargetPool) int {
		return cmp.Compare(*a.Name, *b.Name)
	})
}

func sortHosts(hosts []albsdk.HostConfig) {
	slices.SortFunc(hosts, func(a, b albsdk.HostConfig) int {
		return cmp.Compare(*a.Host, *b.Host)
	})
}
