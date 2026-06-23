# Application Load Balancer Controller Manager User Documentation

The STACKIT Application Load Balancer Controller Manager (ALBCM) exposes HTTP/HTTPS applications by provisioning and configuring managed STACKIT ALBs based on native Kubernetes Ingress resources.

### Enabling the ALB extension
The Application Load Balancer integration is disabled by default and can be activated for your cluster via the SKE-API by setting the enabled field to true inside the applicationLoadBalancer block under extensions:
```JSON
{
  "extensions": {
    "applicationLoadBalancer": {
      "enabled": true
    }
  }
}
```

### Quick start
To expose an application, you need to deploy three core resources: an IngressClass to provision the ALB, a Service to expose your pods, and an Ingress to define the routing.

#### The ALB (IngressClass)
Creating an IngressClass provisions the managed ALB instance. By default, the ALB is assigned a public ephemeral IP address, unless you configure it as an internal ALB or assign a pre-existing static IP via annotations (see *Annotations* section).

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
  type: CLusterIP
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

### Ingress grouping & ALB lifecycle
The controller automatically merges all Ingress resources that reference the same IngressClass onto a single, shared ALB instance. To provision completely isolated ALBs (for example, to separate public and internal traffic or to assign different static IPs) you must create a distinct IngressClass for each one.

If you delete all Ingress resources associated with a specific class, the controller deliberately does not delete the underlying ALB infrastructure. Instead, it transitions the ALB into an empty state that returns HTTP 404s. This behavior preserves your allocated IP address and prevents unnecessary infrastructure recreation delays. To completely delete the ALB and release its associated resources, you must delete the IngressClass.

### Rule ordering
When multiple Ingress resources share an ALB, their routing rules are evaluated chronologically by default, meaning older Ingress resources take precedence based on their CreationTimestamp.

You can override this default order by adding the `alb.stackit.cloud/priority` annotation to an Ingress. Higher integer values are evaluated first, and in the event of a tie, the controller falls back to the creation timestamp.

Note that the top-to-bottom order of paths defined within a single Ingress YAML is non-deterministic. If your application requires strict execution ordering, you must split the rules into separate Ingress resources and assign explicit priority annotations to each.

### TLS and Certificate Rotation
The minimal Ingress example in the Quick Start section shows a plain, unencrypted HTTP configuration. To expose your application securely via HTTPS, the ALB Ingress controller supports TLS termination using standard Kubernetes TLS Secrets.

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

### Supported Ingress Backends
Currently, the STACKIT ALB Ingress controller only supports Kubernetes Service backends. Routing traffic to Resource backends (such as individual Pods or other custom resources) is not supported at this time.

### Validating Webhook
The ALB integration deploys a background validating webhook running alongside the ALB Ingress controller. This webhook automatically reviews incoming Ingress and IngressClass object modifications (creations and updates) preventing invalid properties or conflicting parameters from being applied.

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
The following limitations are imposed directly by the STACKIT ALB API (not the controller itself):
- Maximum targets per pool: An individual target pool can contain a maximum of 250 targets.
- Maximum listeners per ALB: A single ALB instance supports a maximum of 20 listeners.

#### When to watch out for target limits
A "target" in a pool corresponds directly to a worker node in your cluster. If you run a large cluster with a high number of worker nodes, or expect your cluster to dynamically scale to a large size, keep this limit in mind since a single backend Service port mapping cannot route traffic to more than 250 worker nodes simultaneously.

#### When to watch out for the listener limit
Because each IngressClass provisions a dedicated ALB instance, hitting the 20-listener threshold is rarely an issue for a basic setup but becomes a real risk when you start stacking custom ports across multiple applications sharing that same ALB. If your Ingress resources use the `alb.stackit.cloud/http-port` or `alb.stackit.cloud/https-port` annotations to expose different apps on unique custom port numbers, each distinctive port allocates its own listener on the shared ALB instance. This risk compounds quickly when those applications also require TLS encryption; since the controller must keep an extra HTTP listener active alongside the HTTPS listener to smoothly process automated ACME certificate challenges, a single secure app immediately consumes two slots instead of one, accelerating how fast you approach the API limit if multiple unique custom ports are configured.

### Configuration
Configure the STACKIT Application Load Balancer using the following annotations.

| Annotation | Type | Allowed On | Requirement | Description |
| :--- | :--- | :--- | :--- | :--- |
| `alb.stackit.cloud/network-mode` | String | IngressClass | Mandatory | Routing mode (currently only `NodePort` supported). |
| `alb.stackit.cloud/external-address` | String | IngressClass | Optional | Uses a specific STACKIT floating IP instead of an ephemeral one. |
| `alb.stackit.cloud/internal` | Boolean | IngressClass | Optional | If `true`, the ALB is not exposed via a public IP. |
| `alb.stackit.cloud/plan-id` | String | IngressClass | Optional | Sets the service plan for the ALB. |
| `alb.stackit.cloud/priority` | Integer | Ingress | Optional | Defines the evaluation priority of the Ingress. |
| `alb.stackit.cloud/web-application-firewall-name` | String | IngressClass | Optional | Attaches a STACKIT WAF configuration to the listeners. |
| `alb.stackit.cloud/websocket` | Boolean | IngressClass, Ingress | Optional | If `true`, enables WebSocket support for the ALB or specific paths. |
| `alb.stackit.cloud/http-port` | Integer | Ingress | Optional | If set, specifies a custom HTTP port (Default is 80). |
| `alb.stackit.cloud/https-port` | Integer | Ingress | Optional | If set, specifies a custom HTTPS port (Default is 443). |
| `alb.stackit.cloud/https-only` | Boolean | Ingress | Optional | If true, the Ingress will not be reachable via HTTP and only via HTTPS |
| `alb.stackit.cloud/traget-pool-tls-enabled` | Boolean | IngressClass, Ingress, Service | Optional | Enables TLS bridging using OS trusted CAs. |
| `alb.stackit.cloud/traget-pool-tls-custom-ca` | String | IngressClass, Ingress, Service | Optional | Enables TLS bridging with a custom CA. |
| `alb.stackit.cloud/traget-pool-tls-skip-certificate-validation`| Boolean | IngressClass, Ingress, Service | Optional | Enables TLS bridging but skips certificate validation. |

### Known Limitations

#### defaultBackend support
The ALB Ingress Controller currently does not support the `defaultBackend` field on Ingress resources. Customers should avoid relying on this feature as it will be ignored during ALB reconciliation.

