# FlexDaemonsets

## Overview

FlexDaemonsets enhances resource allocation for DaemonSets in heterogeneous Kubernetes clusters. It allows defining resource requests and limits for DaemonSet pods as a percentage of each node's total allocatable resources, rather than using fixed absolute values. This ensures efficient and scalable resource utilization, especially in clusters with nodes of varying sizes (e.g., from 4-core to 64-core VMs).

The solution uses:
- A **Custom Resource Definition (CRD)** named `FlexDaemonsetTemplate` to define percentage-based resource allocation templates.
- An **annotation** on DaemonSets (`flexdaemonsets.xai/resource-template: <template-name>`) to opt-in for this feature.
- A **mutating webhook** that intercepts pod creation for annotated DaemonSets, calculates the appropriate resources based on the node's capacity and the template, and injects these values into the pod specification.

## Project Structure

- `cmd/manager/main.go`: Main entry point for the webhook server.
- `pkg/apis/`: Contains the API type definitions for `FlexDaemonsetTemplate` (e.g., `pkg/apis/flexdaemonsets/v1alpha1/types.go`).
- `pkg/webhook/`: Contains the core mutating webhook logic.
- `pkg/utils/`: Utility functions, including resource calculation.
- `manifests/`: Kubernetes manifests for CRD, RBAC, webhook configuration, deployment, and samples.
- `Dockerfile`: For building the webhook server container image.
- `Makefile`: For build, Docker, and deployment automation.

## Prerequisites

- Go (version 1.24 or higher)
- Docker (for building images)
- kubectl
- A Kubernetes cluster (v1.21+)
- Developers modifying API definitions (`pkg/apis/`) will need `controller-gen` (e.g., `go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0`).

## Building the Project

### Building the Binary
To build the webhook manager binary:
```make manager```
Or:
```make build```
This will output the binary to `./bin/manager`.

## Running Locally (for Development)

You can run the webhook manager locally for development purposes. This setup typically involves running the manager outside the cluster while it communicates with a Kubernetes cluster API server (ensure your `~/.kube/config` is pointed to your cluster).

**Note**: When running locally, the Kubernetes API server will not be able to reach your local webhook server endpoint unless you use a tunneling service (like ngrok) and update the `MutatingWebhookConfiguration`'s `clientConfig.url` field. This local run mode is primarily for testing manager startup and basic interaction with the cluster for fetching Node/CRD objects if the webhook were called.

1.  **Generate TLS Certificates (Self-Signed for Local Use)**:
    The manager requires TLS certificates. The Makefile can generate self-signed ones:
    ```make generate-certs```
    This places certificates in `./_certs/`. The `ca.crt`, `tls.crt`, and `tls.key` will be generated.

2.  **Run the Manager**:
    ```make run```
    This will start the manager, listening on port 9443 (by default) for webhook requests and using certificates from `./_certs` (as specified by the `--cert-dir` flag in the `run` target).

## Building the Docker Image

To build the Docker image for the webhook server:
```make docker-build```
You can customize the image name and tag using the `IMG` variable:
```make docker-build IMG=yourrepo/flexdaemonsets-webhook:yourtag```
The default is `prakarshmodificationusg/flexdaemonsets-webhook:latest`.

To push the image:
```make docker-push IMG=yourrepo/flexdaemonsets-webhook:yourtag```

## Deploying to Kubernetes

The following steps guide you through deploying the FlexDaemonsets webhook to your Kubernetes cluster. Components will be deployed into the `flexdaemonsets-system` namespace.

1.  **Build and Push the Docker Image**:
    Ensure you have built the Docker image (see above) and pushed it to a registry accessible by your Kubernetes cluster.
    ```make docker-build IMG=<your-image-registry>/flexdaemonsets-webhook:<tag>```
    ```make docker-push IMG=<your-image-registry>/flexdaemonsets-webhook:<tag>```
    Make sure the image name in `manifests/deployment.yaml` matches the image you pushed, or update it accordingly. The default is `prakarshmodificationusg/flexdaemonsets-webhook:latest`.

2.  **Generate and Prepare TLS Certificates**:
    The webhook requires TLS certificates. For a production setup, **cert-manager** is highly recommended for managing these certificates automatically.

    If using **cert-manager**:
    - Install cert-manager in your cluster.
    - Annotate the `flexdaemonsets-webhook-svc` Service in `manifests/deployment.yaml` (or create a Certificate resource) to have cert-manager issue a certificate.
    - Cert-manager will typically inject the `caBundle` into the `MutatingWebhookConfiguration` automatically. If so, you can remove the `caBundle` field from `manifests/webhook.yaml` or leave it empty.

    If using **manually generated (self-signed) certificates** (e.g., for testing, using `make generate-certs`):
    - Generate certificates:
      ```make generate-certs```
    - Create the TLS secret in the cluster:
      ```bash
      kubectl create namespace flexdaemonsets-system --dry-run=client -o yaml | kubectl apply -f - # Ensure namespace exists
      kubectl create secret tls flexdaemonsets-webhook-tls \
            --cert=./_certs/tls.crt \
            --key=./_certs/tls.key \
            -n flexdaemonsets-system
      ```
    - **Update `caBundle` in `manifests/webhook.yaml`**:
      The `caBundle` field in `manifests/webhook.yaml` must be populated with the base64-encoded CA certificate (`./_certs/ca.crt`).
      Get the base64 encoded CA:
      ```bash
      cat ./_certs/ca.crt | base64 | tr -d '\n'
      ```
      Paste this value into the `caBundle` field in `manifests/webhook.yaml`.

3.  **Deploy Core Components**:
    This applies the CRD, RBAC roles, MutatingWebhookConfiguration, and the Deployment for the webhook server.
    ```make deploy-manifests```
    If you updated `manifests/webhook.yaml` with the `caBundle`, this command will apply it.

4.  **Deploy Sample Resources (Optional)**:
    Once the webhook is running, you can deploy a sample `FlexDaemonsetTemplate` and a `DaemonSet` that uses it:
    ```make deploy-samples```

## Usage

1.  **Create a `FlexDaemonsetTemplate` Custom Resource**:
    Define your desired percentage-based allocations. See the example template: `manifests/sample-flexdaemonsettemplate.yaml`.
    ```yaml
    # Example from manifests/sample-flexdaemonsettemplate.yaml
    apiVersion: flexdaemonsets.xai/v1alpha1
    kind: FlexDaemonsetTemplate
    metadata:
      name: default-resource-percentages
    spec:
      cpuPercentage: 10 # Request 10% of node's allocatable CPU
      memoryPercentage: 15 # Request 15% of node's allocatable memory
      storagePercentage: 5 # Request 5% of node's allocatable ephemeral-storage
      minCPU: "100m" # Minimum 0.1 CPU
      minMemory: "128Mi" # Minimum 128 MiB Memory
      # minStorage: "1Gi" # Optional: Minimum 1 GiB Ephemeral Storage
    ```
    Apply it: `kubectl apply -f manifests/sample-flexdaemonsettemplate.yaml` (if not already done by `make deploy-samples`).

2.  **Annotate your DaemonSet**:
    Add the annotation `flexdaemonsets.xai/resource-template: <template-name>` to your DaemonSet's metadata.
    See the example: `manifests/sample-daemonset.yaml`.
    ```yaml
    # Example snippet from manifests/sample-daemonset.yaml
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: example-daemonset
      annotations:
        flexdaemonsets.xai/resource-template: "default-resource-percentages" # Points to the FlexDaemonsetTemplate
    # ... rest of DaemonSet spec
    ```
    When new pods for this DaemonSet are created, the webhook will mutate them to include resource requests and limits based on the "default-resource-percentages" template and the specific node they are scheduled on.

## Cleanup

To remove the deployed resources:
```make undeploy```
This will delete the webhook deployment, service, RBAC, webhook configuration, and sample resources. It does not delete the CRD definition itself by default.

To also delete the CRD (which will delete ALL `FlexDaemonsetTemplate` custom resources):
```kubectl delete crd flexdaemonsettemplates.flexdaemonsets.xai```

To remove locally generated certificates and binaries:
```make clean```
