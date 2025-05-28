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

## Getting Started

(Instructions will be added here for building, deploying, and using FlexDaemonsets)

## Prerequisites

- Go (version 1.22 or higher)
- Docker (for building images)
- kubectl
- A Kubernetes cluster (v1.21+)

## (Development) Building and Running Locally

(Instructions to be added)
