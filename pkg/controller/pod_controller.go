package controller

import (
	"context"
	// "encoding/json" // For creating patches if needed - client.Patch with MergeFrom handles this
	"fmt"

	appsv1 "k8s.io/api/apps/v1" // To check DaemonSet owner reference
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1" // Not strictly needed for SchemeGroupVersion
	"k8s.io/apimachinery/pkg/runtime" // Added for runtime.Scheme
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	// "sigs.k8s.io/controller-runtime/pkg/predicate" // If complex predicates are needed

	flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1"
	"github.com/prakarsh-dt/FlexDaemonsets/pkg/utils"
)

const (
	PodApplyTemplateAnnotation = "flexdaemonsets.xai/apply-template" // From webhook
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme // Changed from *ctrl.Scheme
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=flexdaemonsets.xai,resources=flexdaemonsettemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch // Needed to verify DS ownership if desired

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("pod", req.NamespacedName)
	logger.Info("Reconciling Pod")

	pod := &corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod not found, likely deleted.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// 1. Check if pod has the 'apply-template' annotation and NodeName is set
	templateName, ok := pod.Annotations[PodApplyTemplateAnnotation]
	if !ok {
		// No annotation, or already processed. Ignore.
		return ctrl.Result{}, nil
	}
	if templateName == "" {
		logger.Info("Pod has apply-template annotation, but it's empty. Skipping.", "annotation", PodApplyTemplateAnnotation)
		// Potentially remove the empty annotation here if desired
		return ctrl.Result{}, nil
	}
	if pod.Spec.NodeName == "" {
		logger.Info("Pod has apply-template annotation, but NodeName is not yet set. Requeueing.")
		return ctrl.Result{Requeue: true}, nil // Requeue until NodeName is set
	}

	// 2. Verify it's a DaemonSet pod (optional but good for safety)
	isDaemonSetPod := false
	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.APIVersion == appsv1.SchemeGroupVersion.String() && ownerRef.Kind == "DaemonSet" {
			isDaemonSetPod = true
			break
		}
	}
	if !isDaemonSetPod {
		logger.Info("Pod has apply-template annotation but is not a DaemonSet pod. Skipping.")
		// Consider removing the annotation if this is an unexpected state
		// To be safe, we'll remove the annotation to prevent re-reconciliation for non-DS pods with this ann.
		podToUpdate := pod.DeepCopy()
		delete(podToUpdate.Annotations, PodApplyTemplateAnnotation)
		if len(podToUpdate.Annotations) == 0 {
			podToUpdate.Annotations = nil
		}
		if err := r.Patch(ctx, podToUpdate, client.MergeFrom(pod)); err != nil {
			logger.Error(err, "Failed to patch Pod to remove annotation for non-DaemonSet pod")
			return ctrl.Result{}, err
		}
		logger.Info("Removed apply-template annotation from non-DaemonSet pod.")
		return ctrl.Result{}, nil
	}
	logger.Info("Processing DaemonSet pod for resource allocation", "nodeName", pod.Spec.NodeName, "templateName", templateName)

	// 3. Fetch the FlexDaemonsetTemplate
	flexTemplate := &flexdaemonsetsv1alpha1.FlexDaemonsetTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: templateName /* Namespace is empty for Cluster scope */}, flexTemplate)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "FlexDaemonsetTemplate not found. Cannot apply resources. Annotation will remain for now.", "templateName", templateName)
			// Consider removing the annotation from the pod if the template is permanently gone
			return ctrl.Result{}, nil // Don't requeue if template is not found
		}
		logger.Error(err, "Failed to get FlexDaemonsetTemplate", "templateName", templateName)
		return ctrl.Result{}, err
	}

	// 4. Fetch the Node
	node := &corev1.Node{}
	err = r.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, node)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "Node not found. Cannot apply resources. Pod might be deleted soon.", "nodeName", pod.Spec.NodeName)
			return ctrl.Result{}, nil // Don't requeue if node is not found (pod might be deleted soon)
		}
		logger.Error(err, "Failed to get Node", "nodeName", pod.Spec.NodeName)
		return ctrl.Result{}, err
	}

	// 5. Calculate Resources
	calculatedResources, err := utils.CalculatePodResources(&flexTemplate.Spec, node.Status.Allocatable)
	if err != nil {
		logger.Error(err, "Failed to calculate pod resources")
		return ctrl.Result{}, err // Requeue to retry calculation if it was a transient error
	}

	// Prepare for patching
	originalPod := pod.DeepCopy() // For creating a patch
	podToPatch := pod.DeepCopy()

	if len(calculatedResources) == 0 {
		logger.Info("Calculated resources are empty. No changes to apply. Removing annotation.")
		if podToPatch.Annotations != nil { // Ensure annotations map exists
			delete(podToPatch.Annotations, PodApplyTemplateAnnotation)
			if len(podToPatch.Annotations) == 0 {
				podToPatch.Annotations = nil // Remove annotations field if it becomes empty
			}
		}
		// Even if no resources to apply, we must patch to remove the annotation.
		if err := r.Patch(ctx, podToPatch, client.MergeFrom(originalPod)); err != nil {
			logger.Error(err, "Failed to patch Pod to remove annotation after empty resources")
			return ctrl.Result{}, err
		}
		logger.Info("Successfully removed annotation from Pod after processing with empty resources.")
		return ctrl.Result{}, nil
	}

	logger.Info("Successfully calculated pod resources", "resources", fmt.Sprintf("%v", calculatedResources))

	// 6. Apply Resources to Pod and Remove Annotation
	for i := range podToPatch.Spec.Containers {
		if podToPatch.Spec.Containers[i].Resources.Requests == nil {
			podToPatch.Spec.Containers[i].Resources.Requests = corev1.ResourceList{}
		}
		if podToPatch.Spec.Containers[i].Resources.Limits == nil {
			podToPatch.Spec.Containers[i].Resources.Limits = corev1.ResourceList{}
		}
		for resName, quantity := range calculatedResources {
			podToPatch.Spec.Containers[i].Resources.Requests[resName] = quantity
			podToPatch.Spec.Containers[i].Resources.Limits[resName] = quantity // Set limits equal to requests
		}
	}
	for i := range podToPatch.Spec.InitContainers {
		if podToPatch.Spec.InitContainers[i].Resources.Requests == nil {
			podToPatch.Spec.InitContainers[i].Resources.Requests = corev1.ResourceList{}
		}
		if podToPatch.Spec.InitContainers[i].Resources.Limits == nil {
			podToPatch.Spec.InitContainers[i].Resources.Limits = corev1.ResourceList{}
		}
		for resName, quantity := range calculatedResources {
			podToPatch.Spec.InitContainers[i].Resources.Requests[resName] = quantity
			podToPatch.Spec.InitContainers[i].Resources.Limits[resName] = quantity
		}
	}

	if podToPatch.Annotations == nil {
		// This case should ideally not be hit if we found PodApplyTemplateAnnotation earlier,
		// but good for safety.
		podToPatch.Annotations = make(map[string]string)
	}
	delete(podToPatch.Annotations, PodApplyTemplateAnnotation)
	if len(podToPatch.Annotations) == 0 { // If it was the only annotation
		podToPatch.Annotations = nil // Set to nil to remove the annotations field itself
	}

	if err := r.Patch(ctx, podToPatch, client.MergeFrom(originalPod)); err != nil {
		logger.Error(err, "Failed to patch Pod to apply resources and remove annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully applied resources and removed annotation from Pod.")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		// Optionally, use Owns() or Watches() for more complex scenarios,
		// or Predicates to filter events before they hit Reconcile.
		// For example, a predicate could filter for pods with the annotation.
		// WithEventFilter(predicate.AnnotationChangedPredicate{Annotations: []string{PodApplyTemplateAnnotation}}). // Example
		Complete(r)
}
