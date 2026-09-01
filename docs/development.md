# Development

[![Go Reference](https://pkg.go.dev/badge/github.com/stackitcloud/application-load-balancer-controller.svg)](https://pkg.go.dev/github.com/stackitcloud/application-load-balancer-controller)

This document describes how to set up your local development environment for the STACKIT Application Load Balancer Controller.

## Prerequisites
- `make`
- `kubectl`
- `kind` (if testing against a local cluster)
- A STACKIT account with appropriate permissions

## Running Locally Against a STACKIT Cluster (e.g., SKE)
You can run the controller binary locally on your machine while pointing it to a remote Kubernetes cluster running in STACKIT (like SKE).

### 1: Ensure the in-cluster controller is not running
If you are using a STACKIT Kubernetes Engine (SKE) cluster, the ALB controller can be enabled via the API. Make sure you disable the ALB extension for the SKE cluster. Running both simultaneously will result in race conditions and unexpected behavior.

### 2: Create the `cloud.yaml`
The controller requires configuration to know which STACKIT Project and Network the cluster resides in. Create a `cloud.yaml` file in the root directory of this git repository.

```yaml
global:
  region: eu01
  projectId: <project-id>
applicationLoadBalancer:
  networkId: <network-id>
```
> You can find your Project ID and Network ID in the STACKIT Portal for your existing SKE cluster.

### 3: Service Account
The controller needs to interact with the STACKIT API. You must provide it with a Service Account that has the **`alb.admin`** role for the project specified in the `cloud.yaml`.

1. Create a Service Account in the STACKIT Portal (or via CLI) in the STACKIT Project specified in your `cloud.yaml`.
2. Assign the `alb.admin` role to this Service Account.
3. Download the `serviceaccount.json` for this Service Account.
4. Export the `STACKIT_SERVICE_ACCOUNT_KEY_PATH` environment variable to the path of the `serviceaccount.json`.
```bash
export STACKIT_SERVICE_ACCOUNT_KEY_PATH="/path/to/serviceaccount.json"
```

### 4: Run
Ensure your kubeconfig is pointing to the SKE cluster, then run the controller locally:

```bash
make run
```

---

## Running Locally Against a `kind` Cluster

If you don't have an SKE cluster or prefer to develop against a local Kubernetes cluster, you can use `kind` (Kubernetes IN Docker).

> **⚠️ Important Limitation:**
> The actual load balancing will **not** work in this setup. The STACKIT ALB is provisioned in the cloud and cannot route traffic to workloads running locally inside your `kind` Docker containers. However, this setup is sufficient for testing the **controller** (e.g., verifying that the controller properly watches Ingress resources and correctly calls the STACKIT API to create ALB resources).

### 1: Create the `kind` cluster
Spin up a local cluster:
```bash
kind create cluster --name alb-dev
```

### 2: Configure `cloud.yaml`
Because you are not using an existing SKE cluster, you cannot reuse an existing SKE network. You must manually create a dedicated network in STACKIT to simulate the cloud environment:
1. Go to the STACKIT Portal.
2. Create a Project (or use an existing one).
3. Create a Network inside that Project.
4. Create your `cloud.yaml` using the IDs from the Project and Network you just created:

```yaml
global:
  region: eu01
  projectId: <project-id>
applicationLoadBalancer:
  networkId: <network-id>
```

### 3: Service Account
The controller needs to interact with the STACKIT API. You must provide it with a Service Account that has the **`alb.admin`** role for the project specified in the `cloud.yaml`.

1. Create a Service Account in the STACKIT Portal (or via CLI) in the STACKIT Project specified in your `cloud.yaml`.
2. Assign the `alb.admin` role to this Service Account.
3. Download the `serviceaccount.json` for this Service Account.
4. Export the `STACKIT_SERVICE_ACCOUNT_KEY_PATH` environment variable to the path of the `serviceaccount.json`.
```bash
export STACKIT_SERVICE_ACCOUNT_KEY_PATH="/path/to/serviceaccount.json"
```

### 4: Run
Ensure your kubeconfig points to your `kind` cluster, then start the controller:

```bash
make run
```

---

## Code Verification (Linters, Formatting, Tests)

To maintain code quality, the repository uses linters and automated checks. Before opening a Pull Request or committing your code, you should always verify that your changes pass these checks.

Run the following command from the root of the repository:
```bash
make verify
```

## Testing STACKIT ALB Ingress Scenarios

The `../example-ingress/` directory contains a set of test manifests to verify the routing capabilities of the ALB controller.

These manifests deploy an `IngressClass` (which provisions the ALB) along with several deployments using the `podinfo` image. Each deployment is configured to return a unique JSON message, making it easy to identify which backend processed your request.

### Deploy the Examples

You can apply the entire directory at once. The numbering ensures the `IngressClass` is created first, followed by the specific routing scenarios:

```bash
kubectl apply -f docs/example-ingress/
```

The deployed scenarios include:
* `1_ingress-path.yaml`: Path-based routing (`/path1` vs `/path2` on `example.com`).
* `2_ingress-host.yaml`: Host-based routing (`alb.example.com`).
* `3_ingress-port.yaml`: Custom ALB listener ports (`port.example.com:8080`).
* `4_ingress-prio.yaml`: Rule evaluation priority (ensuring `prio.example.com` routes to the higher priority backend).
* `5_ingress-secure.yaml`: TLS termination using a dynamically generated self-signed certificate (`secure.example.com`).

### Retrieve the ALB IP

Wait a few moments for the controller to provision the load balancer and assign a public IP address, then export it to an environment variable:

```bash
export ALB_IP=$(kubectl get ingress ing-alb-example -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "ALB IP is: $ALB_IP"
```

### Verify Routing (No DNS Required)

You can test all routing rules without creating actual DNS records by using `curl` with the `--resolve` flag. This forces `curl` to route the traffic to your ALB's IP while preserving the correct `Host` header and SNI (Server Name Indication) required for TLS handshakes.

```bash
# 1. Path-based routing
curl -s -H "Host: example.com" http://$ALB_IP/path1
curl -s -H "Host: example.com" http://$ALB_IP/path2

# 2. Host-based routing
curl -s -H "Host: alb.example.com" http://$ALB_IP/

# 3. Custom Port (8080)
curl -s -H "Host: port.example.com:8080" http://$ALB_IP:8080/

# 4. Priority Evaluation (Should return the HIGHER priority app)
curl -s -H "Host: prio.example.com" http://$ALB_IP/

# 5. Secure/TLS Endpoint (Using -k to bypass local validation of the self-signed cert)
curl -s -k -H "Host: secure.example.com" https://$ALB_IP/
```

### 4. Cleanup

To tear down the tests, simply delete everything again:

```bash
kubectl delete -f docs/example-ingress/
```
