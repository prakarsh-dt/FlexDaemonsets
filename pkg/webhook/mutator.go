package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1"
	"github.com/prakarsh-dt/FlexDaemonsets/pkg/utils" // Import the utils package
)

const (
	FlexDaemonsetTemplateAnnotation = "flexdaemonsets.xai/resource-template"
)

var log = ctrl.Log.WithName("webhook").WithName("PodMutator")

// PodMutator mutates Pods
type PodMutator struct {
	Client  client.Client
	Decoder admission.Decoder // Correct interface type for v0.18.0
}

// Handle is the main entry point for the mutating webhook.
func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	// It's important that m.Decoder is not nil. It's initialized in main.go.
	if m.Decoder == nil {
		log.Error(fmt.Errorf("decoder not initialized"), "Decoder is nil")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("decoder not initialized"))
	}
	err := m.Decoder.Decode(req, pod) // Ensure call is on the interface value
	if err != nil {
		log.Error(err, "Failed to decode pod from admission request")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Create a logger specific to this request
	requestLogger := log.WithValues(
		"podUID", req.UID,
		"podKind", req.Kind.Kind,
		"podNamespace", req.Namespace,
		"podName", req.Name, // req.Name should be populated for existing pods, use pod.GenerateName for new ones if empty
		"operation", req.Operation,
	)
	if req.Name == "" && pod.GenerateName != "" { // More accurate logging for pods being created
	    requestLogger = log.WithValues(
		    "podUID", req.UID,
		    "podKind", req.Kind.Kind,
		    "podNamespace", req.Namespace,
		    "podGenerateName", pod.GenerateName, 
		    "operation", req.Operation,
	    )
    }


	// Check if the pod is part of a DaemonSet.
	isDaemonSetPod := false
	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.Kind == "DaemonSet" && ownerRef.APIVersion == "apps/v1" { // Be specific about DaemonSet API version
			isDaemonSetPod = true
			break
		}
	}

	if !isDaemonSetPod {
		requestLogger.Info("Pod is not owned by a DaemonSet, skipping mutation.")
		return admission.Allowed("Pod is not owned by a DaemonSet.")
	}
	
	requestLogger.Info("Pod is owned by a DaemonSet, checking for FlexDaemonset annotation.")

	templateName, ok := pod.Annotations[FlexDaemonsetTemplateAnnotation]
	if !ok {
		requestLogger.Info("FlexDaemonset annotation not found, skipping mutation.", "annotation", FlexDaemonsetTemplateAnnotation)
		return admission.Allowed("FlexDaemonset annotation not found.")
	}

	if templateName == "" {
		requestLogger.Info("FlexDaemonset annotation value is empty, skipping mutation.", "annotation", FlexDaemonsetTemplateAnnotation)
		return admission.Allowed("FlexDaemonset annotation value is empty.")
	}

	requestLogger.Info("FlexDaemonset annotation found.", "templateName", templateName)

	// 1. Fetch the FlexDaemonsetTemplate CRD instance
	template := &flexdaemonsetsv1alpha1.FlexDaemonsetTemplate{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: templateName}, template) // Cluster-scoped, so no namespace
	if err != nil {
		requestLogger.Error(err, "Failed to get FlexDaemonsetTemplate.", "templateName", templateName)
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get FlexDaemonsetTemplate '%s': %w", templateName, err))
	}
	requestLogger.Info("Successfully fetched FlexDaemonsetTemplate.", "templateName", templateName)

	// 2. Fetch the Node object where the pod is scheduled
	if pod.Spec.NodeName == "" {
		requestLogger.Info("Pod.Spec.NodeName is not set. Cannot determine node for resource allocation. Skipping mutation.")
		return admission.Allowed("Pod NodeName not specified at time of mutation.")
	}

	node := &corev1.Node{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, node)
	if err != nil {
		requestLogger.Error(err, "Failed to get Node.", "nodeName", pod.Spec.NodeName)
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get Node '%s': %w", pod.Spec.NodeName, err))
	}
	requestLogger.Info("Successfully fetched Node.", "nodeName", node.Name, "allocatableCPU", node.Status.Allocatable.Cpu().String(), "allocatableMemory", node.Status.Allocatable.Memory().String())

	// 3. Calculate desired resources
	calculatedPodResources, err := utils.CalculatePodResources(&template.Spec, node.Status.Allocatable)
	if err != nil {
		requestLogger.Error(err, "Failed to calculate pod resources.")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to calculate pod resources: %w", err))
	}

	if len(calculatedPodResources) == 0 {
		requestLogger.Info("Calculated resources are empty (e.g. all percentages and minimums are zero). No mutation will be applied.")
		return admission.Allowed("Calculated resources are empty, no changes.")
	}
	
	requestLogger.Info("Successfully calculated pod resources.", "calculatedResources", fmt.Sprintf("%v", calculatedPodResources))

	// 4. Create JSON patch
	mutatedPod := pod.DeepCopy() // It's crucial to copy the pod object before mutating it

	// Apply calculated resources to all containers.
	// For DaemonSets, typically all containers get the same resource allocation,
	// or specific container names could be targeted if the CRD was extended.
	for i := range mutatedPod.Spec.Containers {
		if mutatedPod.Spec.Containers[i].Resources.Requests == nil {
			mutatedPod.Spec.Containers[i].Resources.Requests = corev1.ResourceList{}
		}
		if mutatedPod.Spec.Containers[i].Resources.Limits == nil {
			mutatedPod.Spec.Containers[i].Resources.Limits = corev1.ResourceList{}
		}

		for resourceName, quantity := range calculatedPodResources {
			mutatedPod.Spec.Containers[i].Resources.Requests[resourceName] = quantity
			mutatedPod.Spec.Containers[i].Resources.Limits[resourceName] = quantity // Setting limits equal to requests
		}
		requestLogger.Info("Applied resources to container.", "containerName", mutatedPod.Spec.Containers[i].Name, "resources", fmt.Sprintf("%v", calculatedPodResources))
	}
	
	// Also apply to init containers, if any
	for i := range mutatedPod.Spec.InitContainers {
		if mutatedPod.Spec.InitContainers[i].Resources.Requests == nil {
			mutatedPod.Spec.InitContainers[i].Resources.Requests = corev1.ResourceList{}
		}
		if mutatedPod.Spec.InitContainers[i].Resources.Limits == nil {
			mutatedPod.Spec.InitContainers[i].Resources.Limits = corev1.ResourceList{}
		}

		for resourceName, quantity := range calculatedPodResources {
			mutatedPod.Spec.InitContainers[i].Resources.Requests[resourceName] = quantity
			mutatedPod.Spec.InitContainers[i].Resources.Limits[resourceName] = quantity // Setting limits equal to requests
		}
		requestLogger.Info("Applied resources to init container.", "containerName", mutatedPod.Spec.InitContainers[i].Name, "resources", fmt.Sprintf("%v", calculatedPodResources))
	}


	marshaledPod, err := json.Marshal(mutatedPod)
	if err != nil {
		requestLogger.Error(err, "Failed to marshal mutated pod.")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	requestLogger.Info("Successfully mutated pod. Returning patch response.")
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// Decoder will be injected by the manager.
// Ensure this struct implements admission.DecoderAware if you want the manager to inject it.
// For controller-runtime v0.17+, explicit injection via a constructor or direct setting in main.go is common.

var _ admission.Handler = &PodMutator{}
// var _ admission.DecoderInjector = &PodMutator{} // DecoderInjector was removed in controller-runtime v0.17+
