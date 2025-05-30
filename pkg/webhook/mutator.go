package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	appsv1 "k8s.io/api/apps/v1" // Added
	corev1 "k8s.io/api/core/v1"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1" // Not strictly needed if using appsv1.SchemeGroupVersion.String()
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	// flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1" // Removed
	"github.com/prakarsh-dt/FlexDaemonsets/pkg/utils"
)

const (
	PodApplyTemplateAnnotation = "flexdaemonsets.xai/apply-template" // Annotation to be placed on Pod
)

var log = ctrl.Log.WithName("webhook").WithName("PodMutator")

// PodMutator mutates Pods by annotating them if their owning DaemonSet is annotated.
type PodMutator struct {
	Client  client.Client
	Decoder admission.Decoder // Correct interface type for v0.18.0 (as determined previously)
}

// Handle is the main entry point for the mutating webhook.
func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}

	if m.Decoder == nil {
		log.Error(fmt.Errorf("decoder not initialized"), "Decoder is nil in PodMutator")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("decoder not initialized"))
	}

	err := m.Decoder.Decode(req, pod)
	if err != nil {
		log.Error(err, "Failed to decode pod from admission request")
		return admission.Errored(http.StatusBadRequest, err)
	}

	requestLogger := log.WithValues(
		"podUID", req.UID,
		"podKind", req.Kind.Kind,
		"podNamespace", req.Namespace,
		"podName", req.Name,
		"podGenerateName", pod.GenerateName,
		"operation", req.Operation,
	)

	daemonSetName := ""
	foundDaemonSetOwner := false
	for _, ownerRef := range pod.OwnerReferences {
		// Check if SchemeGroupVersion is correctly used. For apps/v1, it's appsv1.SchemeGroupVersion.String()
		if ownerRef.APIVersion == appsv1.SchemeGroupVersion.String() && ownerRef.Kind == "DaemonSet" {
			daemonSetName = ownerRef.Name
			foundDaemonSetOwner = true
			requestLogger.Info("Pod owned by DaemonSet", "daemonSetName", daemonSetName)
			break
		}
	}

	if !foundDaemonSetOwner {
		requestLogger.Info("Pod is not owned by a DaemonSet, skipping.")
		return admission.Allowed("Pod is not owned by a DaemonSet.")
	}

	// Fetch the owning DaemonSet
	daemonSet := &appsv1.DaemonSet{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: daemonSetName, Namespace: req.Namespace}, daemonSet)
	if err != nil {
		requestLogger.Error(err, "Failed to get owning DaemonSet", "daemonSetName", daemonSetName, "namespace", req.Namespace)
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get owning DaemonSet %s/%s: %w", req.Namespace, daemonSetName, err))
	}
	requestLogger.Info("Successfully fetched owning DaemonSet", "daemonSetName", daemonSetName)

	// Read annotation from DaemonSet
	templateNameFromDSAnnotation, ok := daemonSet.Annotations[utils.FlexDaemonsetTemplateAnnotation]
	if !ok || templateNameFromDSAnnotation == "" {
		requestLogger.Info("Owning DaemonSet does not have the required annotation or annotation is empty.", "daemonSetName", daemonSetName, "annotation", utils.FlexDaemonsetTemplateAnnotation)
		return admission.Allowed("Owning DaemonSet is not annotated for flex resource allocation.")
	}
	requestLogger.Info("Found template annotation on DaemonSet", "templateName", templateNameFromDSAnnotation)

	// Mutate Pod to Add Annotation
	mutatedPod := pod.DeepCopy()
	if mutatedPod.Annotations == nil {
		mutatedPod.Annotations = make(map[string]string)
	}
	mutatedPod.Annotations[PodApplyTemplateAnnotation] = templateNameFromDSAnnotation
	requestLogger.Info("Annotating Pod for FlexDaemonset controller processing", "podAnnotation", PodApplyTemplateAnnotation, "templateName", templateNameFromDSAnnotation)

	// Create and Return JSON Patch
	marshaledPod, err := json.Marshal(mutatedPod)
	if err != nil {
		requestLogger.Error(err, "Failed to marshal mutated pod for patch")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

var _ admission.Handler = &PodMutator{}
