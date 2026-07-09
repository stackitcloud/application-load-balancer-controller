# Application Load Balancer Controller User Documentation

The STACKIT Application Load Balancer Controller (ALBC) exposes HTTP/HTTPS applications by provisioning and configuring managed STACKIT ALBs based on native Kubernetes Ingress resources.

### Quick start

To expose an application, you need to deploy three core resources: an IngressClass to provision the ALB, a Service to expose your pods, and an Ingress to define the routing.

#### The ALB (IngressClass)

Creating an IngressClass provisions the managed ALB instance. By default, the ALB is assigned a public ephemeral IP address, unless you configure it as an internal ALB or assign a pre-existing static IP via annotations (see [Annotations](#configuration)).

If no Ingress resources are currently linked to this class, the ALB acts as an empty listener that returns an HTTP 404 Not Found.

You must include the `alb.stackit.cloud/network-mode: "NodePort"` annotation on the IngressClass. This is mandatory because it tells the ALB how to reach your cluster, instructing the load balancer to route incoming traffic directly to the node ports on your cluster's worker nodes. At the moment, `NodePort` is the only supported network mode.

```YAML
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: stackit-alb
  annotations:
    alb.stackit.cloud/network-mode: "NodePort"
spec:
  controller: stackit.cloud/alb-ingress
```

#### The backend (Service)

Expose your application pods using a Kubernetes Service.

```YAML
apiVersion: v1
kind: Service
metadata:
  name: service-a
  namespace: default
  labels:
    app: service-a
spec:
  type: NodePort
  ports:
  - port: 80
    protocol: TCP
    targetPort: 80
  selector:
    app: service-a
```

#### The routing (Ingress)

Create the Ingress resource to route incoming traffic to your backend Service. Link it to your ALB by referencing the IngressClass name.

```YAML
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: service-ingress
  namespace: default
spec:
  ingressClassName: stackit-alb
  rules:
  - host: app.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: service-a
            port:
              number: 80
```

The path type `ImplementationSpecific` is currently treated as `Exact`. Regex matchers are not allowed.

The annotation `kubernetes.io/ingress.class` is not supported. Use `.spec.ingressClassName` instead.

### Ingress grouping & ALB lifecycle

The controller automatically merges all Ingress resources that reference the same IngressClass onto a single, shared ALB instance. To provision completely isolated ALBs (for example, to separate public and internal traffic or to assign different static IPs) you must create a distinct IngressClass for each one.

If you delete all Ingress resources associated with a specific class, the controller deliberately does not delete the underlying ALB infrastructure. Instead, it transitions the ALB into an empty state that returns HTTP 404s. This behavior preserves your allocated IP address and prevents unnecessary infrastructure recreation delays. To completely delete the ALB and release its associated resources, you must delete the IngressClass.

### Rule precedence

When multiple Ingress resources share an ALB, their routing rules are evaluated chronologically by default, meaning older Ingress resources take precedence based on their CreationTimestamp. The precedence is only important if not all rules can be admitted to the load balancer. You can override this default order by adding the `alb.stackit.cloud/priority` annotation to an Ingress. Higher integer values are evaluated first, and in the event of a tie, the controller falls back to the creation timestamp. Within an ingress, rules are evaluated top to bottom.

After the admission phase, rules are ordered differently to prefer more specific matchers. Using the following criteria:
- By path type: `Exact`, `ImplementationSpecific`, `Prefix`
- By path length, longest first
- By path lexicographically

Note, that an ingress with a higher priority does not match first. It only means that it is preferred if not all rules can be admitted to the load balancer.

### TLS and Certificate Rotation

The minimal Ingress example in the Quick Start section shows an HTTP configuration. To expose your application securely via HTTPS, the ALB Ingress controller supports TLS termination using standard Kubernetes TLS Secrets.

This functionality integrates seamlessly with tools like cert-manager to automate certificate provisioning and renewal. When a Secret is referenced in the Ingress `tls` block, the controller automatically handles the certificate deployment on the ALB. It continuously monitors the Secret for changes, such as during automated certificate rotation, and updates the ALB without manual intervention. Once a TLS Secret is no longer referenced by any Ingress on that ALB, it is automatically removed.

By default, standard unencrypted HTTP traffic will still be possible alongside HTTPS to make automated ACME certificate challenges possible. If you want to restrict traffic so the Ingress is not reachable via standard HTTP, you can add the `alb.stackit.cloud/https-only: "true"` annotation to your Ingress or IngressClass resource.

**Important:** Because the ALB selects certificates purely based on Server Name Indication (SNI), a certificate from one Ingress can impact others sharing the same ALB. To prevent unintended certificate serving, ensure your Ingress resources have no overlapping DNS names, use distinct ports, or separate them entirely using distinct IngressClasses.

```YAML
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: secure-ingress
  namespace: default
spec:
  ingressClassName: stackit-alb
  tls:
  - hosts:
    - secure.example.com
    secretName: my-tls-secret
  rules:
  - host: secure.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: service-a
            port:
              number: 80
```

The field `Ingress.spec.tls.hosts` is ignored by the controller. The ALB takes the host information directly from the certificates.

### Supported Ingress Backends

Currently, the STACKIT ALB Ingress controller only supports Kubernetes Service backends. Routing traffic to Resource backends (such as individual Pods or other custom resources) is not supported at this time.

### Optimizing traffic with externalTrafficPolicy

By default, Kubernetes Services use `externalTrafficPolicy: Cluster`. Under this policy, every worker node passes the ALB's health checks because kube-proxy accepts the incoming traffic on any node and automatically routes it over the internal network to a node that is actually running your pod.

However, this setup can cause issues when pods are terminating or nodes are scaling down. Because the ALB relies on passively probing the data port, it only detects failures through connection timeouts. This means the ALB might still send traffic to a node while its pods are actively shutting down, or during the brief window after a node goes down but before the next health probe officially fails. Routing new user requests during this delay results in dropped connections and timeout errors.

To prevent these dropped connections during deployments and cluster downscaling, you can change your Service to use `externalTrafficPolicy: Local`.

**Important:** For this to work, your backend Service must be defined as `type: LoadBalancer`. While Kubernetes technically allows setting `externalTrafficPolicy: Local` on a standard `NodePort` Service, it will not generate the required `healthCheckNodePort`. Additionally, because `type: LoadBalancer` natively triggers the cluster's default Cloud Controller Manager to automatically provision a Network Load Balancer (NLB), you must also specify the `loadBalancerClass` field. This ensures the STACKIT ALB controller takes an ownership of the service and prevents an unwanted NLB from being created.

When correctly configured, Kubernetes exposes a dedicated health check port (healthCheckNodePort) on every node. The STACKIT ALB controller automatically detects this and reconfigures the ALB to probe this health port instead of the standard data port. If a node lacks active pods, or if its pods enter a Terminating state, the health port instantly returns an HTTP 503 error. The ALB registers the failure immediately and pulls the node out of rotation before user connections can be dropped. As an added benefit, this policy also eliminates internal network hops and preserves the client's original IP address.

To enable this behavior, update your backend Service configuration:
```YAML
apiVersion: v1
kind: Service
metadata:
  name: service-a
  namespace: default
  labels:
    app: service-a
spec:
  type: LoadBalancer
  loadBalancerClass: alb
  externalTrafficPolicy: Local
  ports:
  - port: 80
    protocol: TCP
    targetPort: 80
  selector:
    app: service-a
```

### Limits

The following limitations are imposed by the STACKIT ALB API:
- Maximum targets per pool: An individual target pool can contain a maximum of 250 targets.
- Maximum target pools per ALB: A single ALB instance supports a maximum of 20 target pool.

#### Target limit

A target corresponds directly to a node in your Kubernetes cluster. 
The number of nodes must not exceed 250.

#### Target pool limit

Each service reference in each ingress translates to a target pool. 
If two ingresses or paths within an ingress reference the same service and port the controller will create two target pools.
If all ingresses of a single ingress class exceed 20 target pools then the first 20 are admitted based on their [precedence](#rule-precedence).

### Configuration

Configure the STACKIT Application Load Balancer using the following annotations.

| Annotation | Type | Allowed On | Requirement | Description |
| :--- | :--- | :--- | :--- | :--- |
| `alb.stackit.cloud/network-mode` | String | IngressClass | Mandatory | Routing mode (currently only `NodePort` supported). |
| `alb.stackit.cloud/external-address` | String | IngressClass | Optional | Uses a specific STACKIT floating IP instead of an ephemeral one. |
| `alb.stackit.cloud/internal` | Boolean | IngressClass | Optional | If `true`, the ALB is not exposed via a public IP. |
| `alb.stackit.cloud/plan-id` | String | IngressClass | Optional | Sets the service plan for the ALB. |
| `alb.stackit.cloud/priority` | Integer | Ingress | Optional | Defines the evaluation priority of the Ingress. Higher number takes priority. Defaults to zero. |
| `alb.stackit.cloud/web-application-firewall-name` | String | IngressClass | Optional | Attaches a STACKIT WAF configuration to the listeners. |
| `alb.stackit.cloud/websocket` | Boolean | IngressClass, Ingress | Optional | If `true`, enables WebSocket support for the ALB or specific paths. |
| `alb.stackit.cloud/http-port` | Integer | Ingress | Optional | If set, specifies a custom HTTP port (Default is 80). |
| `alb.stackit.cloud/https-port` | Integer | Ingress | Optional | If set, specifies a custom HTTPS port (Default is 443). |
| `alb.stackit.cloud/https-only` | Boolean | Ingress | Optional | If true, the Ingress will not be reachable via HTTP and only via HTTPS |
| `alb.stackit.cloud/traget-pool-tls-enabled` | Boolean | IngressClass, Ingress, Service | Optional | Enables TLS bridging using OS trusted CAs. |
| `alb.stackit.cloud/traget-pool-tls-custom-ca` | String | IngressClass, Ingress, Service | Optional | Enables TLS bridging with a custom CA. |
| `alb.stackit.cloud/traget-pool-tls-skip-certificate-validation`| Boolean | IngressClass, Ingress, Service | Optional | Enables TLS bridging but skips certificate validation. |
| `alb.stackit.cloud/allowed-source-ranges`| String | IngressClass | Accepts a comma-separated list of IP ranges. E.g. 10.0.0.0/24,1.2.3.4/32. If unset, all IPs are allowed. |

### Known Limitations

#### Backend Services must be of type `NodePort`

The controller currently only supports routing traffic to backend Services of `type: NodePort` (or `LoadBalancer`, which also allocates a NodePort). Services of type `ClusterIP` cannot be used as backends because the ALB needs a node-reachable port to forward traffic to.

#### Support for `defaultBackend`

The ALB Ingress Controller currently does not support the `defaultBackend` field on Ingress resources. Customers should avoid relying on this feature as it will be ignored during ALB reconciliation.

#### Dummy listener for empty application load balancers

Currently, application load balancers require at least one listener.
If the ingress class results in zero listeners, a dummy listener on port 80 is added to be able to create the load balancer.
This listener always returns the HTTP status code 404.
Common scenarios where this can happen is when there are zero ingresses or an HTTPS-only load balancer does not have any certificates yet.
