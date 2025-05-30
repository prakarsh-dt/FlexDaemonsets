package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1"
)

const (
	FlexDaemonSetNodePodControllerName = "FlexDaemonSetNodePodController"
	// LabelManagedBy is used to identify pods managed by this controller
	LabelManagedBy = "flexdaemonsets.xai/managed-by"
	// LabelOwnerCR is used to identify the owner FDNP CR
	LabelOwnerCR = "flexdaemonsets.xai/owner-cr"

	PhasePending        = "Pending"
	PhaseCreatingPod    = "CreatingPod"
	PhaseActive         = "Active"
	PhaseConflict       = "ConflictWithDaemonSet"
	PhaseYielded        = "Yielded"
	PhaseFailed         = "Failed"
	PhaseTerminating    = "Terminating"
)

// FlexDaemonSetNodePodReconciler reconciles a FlexDaemonSetNodePod object
type FlexDaemonSetNodePodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=flexdaemonsets.xai,resources=flexdaemonsetnodepods,verbs=get;list;watch;update;patch;delete
//+kubebuilder:rbac:groups=flexdaemonsets.xai,resources=flexdaemonsetnodepods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch // May not be strictly needed if all info is in FDNP

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FlexDaemonSetNodePodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("flexdaemonsetnodepod", req.NamespacedName)

	fdnp := &flexdaemonsetsv1alpha1.FlexDaemonSetNodePod{}
	if err := r.Get(ctx, req.NamespacedName, fdnp); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("FlexDaemonSetNodePod resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get FlexDaemonSetNodePod")
		return ctrl.Result{}, err
	}

	currentPhase := fdnp.Status.Phase
	defer func() {
		if fdnp.Status.Phase != currentPhase || fdnp.Status.ObservedGeneration != fdnp.Generation {
			fdnp.Status.ObservedGeneration = fdnp.Generation
			if err := r.Status().Update(ctx, fdnp); err != nil {
				logger.Error(err, "Failed to update FlexDaemonSetNodePod status")
			}
		}
	}()

	// Handle Deletion
	if !fdnp.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("FlexDaemonSetNodePod is being deleted.", "name", fdnp.Name)
		fdnp.Status.Phase = PhaseTerminating
		// Pods owned by this FDNP should be garbage collected due to OwnerReferences.
		// If finalizers were used, this is where they'd be handled.
		// For now, no finalizers.
		return ctrl.Result{}, nil
	}

	// Fetch the original DaemonSet
	originalDS := &appsv1.DaemonSet{}
	dsNamespacedName := types.NamespacedName{
		Namespace: fdnp.Spec.DaemonSetNamespace,
		Name:      fdnp.Spec.DaemonSetName,
	}
	if err := r.Get(ctx, dsNamespacedName, originalDS); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "Original DaemonSet not found", "daemonset", dsNamespacedName.String())
			fdnp.Status.Phase = PhaseFailed
			fdnp.Status.Message = fmt.Sprintf("Original DaemonSet %s not found", dsNamespacedName.String())
			return ctrl.Result{Requeue: true}, nil // Requeue as DS might appear later
		}
		logger.Error(err, "Failed to get original DaemonSet", "daemonset", dsNamespacedName.String())
		return ctrl.Result{}, err
	}

	// Check for Conflicting DaemonSet Pod (a pod directly owned by the DaemonSet on the target node)
	var dsOwnedPods corev1.PodList
	dsPodSelector := labels.SelectorFromSet(originalDS.Spec.Selector.MatchLabels)
	if err := r.List(ctx, &dsOwnedPods, client.InNamespace(fdnp.Spec.DaemonSetNamespace), client.MatchingLabelsSelector{Selector: dsPodSelector}); err != nil {
		logger.Error(err, "Failed to list pods for original DaemonSet", "daemonSet", originalDS.Name)
		return ctrl.Result{}, err
	}

	for _, pod := range dsOwnedPods.Items {
		if pod.Spec.NodeName == fdnp.Spec.NodeName {
			// Check if this pod is actually owned by the DaemonSet (and not by an FDNP or other controller)
			for _, ownerRef := range pod.OwnerReferences {
				if ownerRef.APIVersion == appsv1.SchemeGroupVersion.String() && ownerRef.Kind == "DaemonSet" && ownerRef.Name == originalDS.Name {
					logger.Info("Conflicting DaemonSet pod found on node. Deleting FlexDaemonSetNodePod.", "nodeName", fdnp.Spec.NodeName, "conflictingPod", pod.Name)
					fdnp.Status.Phase = PhaseConflict
					fdnp.Status.Message = fmt.Sprintf("Conflicting pod %s from DaemonSet %s found on node %s", pod.Name, originalDS.Name, fdnp.Spec.NodeName)
					// Deleting the FDNP CR itself. Its owned pod will be GC'd.
					if err := r.Delete(ctx, fdnp); err != nil {
						logger.Error(err, "Failed to delete FlexDaemonSetNodePod due to conflict")
						return ctrl.Result{}, err
					}
					logger.Info("FlexDaemonSetNodePod deleted due to conflict.", "name", fdnp.Name)
					return ctrl.Result{}, nil
				}
			}
		}
	}
	
	// Check for Existing Managed Pod (owned by this FDNP instance)
	managedPodName := r.generateManagedPodName(fdnp)
	managedPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: managedPodName, Namespace: fdnp.Namespace}, managedPod)
	if err == nil {
		// Managed pod exists
		logger.Info("Found existing managed pod", "podName", managedPod.Name)
		// TODO: Compare its resources and other critical specs with fdnp.Spec.
		// For now, assume if pod exists and is owned by fdnp, it's correctly configured.
		// A more robust check would verify owner references and key spec fields.
		isOwned := false
		for _, ref := range managedPod.OwnerReferences {
			if ref.UID == fdnp.UID {
				isOwned = true
				break
			}
		}
		if !isOwned {
			logger.Error(fmt.Errorf("pod %s exists but not owned by this FDNP", managedPodName), "Ownership mismatch, potential conflict or adoption needed.")
			fdnp.Status.Phase = PhaseFailed
			fdnp.Status.Message = "Found pod with same name but not owned by this FDNP."
			// This scenario needs careful handling - potentially delete the non-owned pod if naming convention is strict,
			// or fail the FDNP. For now, fail.
			return ctrl.Result{Requeue: true}, nil
		}

		fdnp.Status.Phase = PhaseActive
		fdnp.Status.Message = fmt.Sprintf("Pod %s is active on node %s", managedPod.Name, fdnp.Spec.NodeName)
		return ctrl.Result{}, nil
	}
	
	if !errors.IsNotFound(err) {
		logger.Error(err, "Failed to get managed pod", "podName", managedPodName)
		return ctrl.Result{}, err
	}

	// No Managed Pod Exists (and no conflicting DS pod), proceed to create
	logger.Info("No managed pod found, creating a new one.", "targetNode", fdnp.Spec.NodeName)
	fdnp.Status.Phase = PhaseCreatingPod

	newPod, err := r.constructPodForFlexDaemonSetNodePod(fdnp, originalDS)
	if err != nil {
		logger.Error(err, "Failed to construct pod for FlexDaemonSetNodePod")
		fdnp.Status.Phase = PhaseFailed
		fdnp.Status.Message = fmt.Sprintf("Failed to construct pod: %v", err)
		return ctrl.Result{}, err // Error is likely not recoverable by requeue if construction fails
	}

	if err := r.Create(ctx, newPod); err != nil {
		if errors.IsAlreadyExists(err) {
			logger.Info("Managed pod already exists, though Get failed earlier. Requeueing.", "podName", newPod.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Error(err, "Failed to create new managed pod", "podName", newPod.Name)
		fdnp.Status.Phase = PhaseFailed
		fdnp.Status.Message = fmt.Sprintf("Failed to create pod %s: %v", newPod.Name, err)
		return ctrl.Result{}, err
	}

	logger.Info("Successfully created managed pod", "podName", newPod.Name, "nodeName", fdnp.Spec.NodeName)
	fdnp.Status.Phase = PhaseActive
	fdnp.Status.Message = fmt.Sprintf("Pod %s created and active on node %s", newPod.Name, fdnp.Spec.NodeName)
	
	return ctrl.Result{}, nil
}

func (r *FlexDaemonSetNodePodReconciler) generateManagedPodName(fdnp *flexdaemonsetsv1alpha1.FlexDaemonSetNodePod) string {
	return fmt.Sprintf("%s-pod", fdnp.Name) // Example: my-fdnp-cr-pod
}

func (r *FlexDaemonSetNodePodReconciler) constructPodForFlexDaemonSetNodePod(
	fdnp *flexdaemonsetsv1alpha1.FlexDaemonSetNodePod,
	ds *appsv1.DaemonSet,
) (*corev1.Pod, error) {

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.generateManagedPodName(fdnp),
			Namespace: fdnp.Namespace,
			Labels:    make(map[string]string),
			Annotations: make(map[string]string),
		},
		Spec: *ds.Spec.Template.Spec.DeepCopy(), // Start with a copy of the DaemonSet's pod spec
	}

	// Set OwnerReference to the FDNP
	if err := controllerutil.SetControllerReference(fdnp, pod, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference on pod: %w", err)
	}

	// Add identifying labels
	pod.Labels[LabelManagedBy] = FlexDaemonSetNodePodControllerName
	pod.Labels[LabelOwnerCR] = fdnp.Name
	// Copy labels from FDNP annotations/labels if desired
	for k, v := range fdnp.Labels {
		if _, exists := pod.Labels[k]; !exists { // Avoid overwriting controller-specific labels
			pod.Labels[k] = v
		}
	}
	// Copy annotations from DS template, then from FDNP
    for k,v := range ds.Spec.Template.Annotations {
        pod.Annotations[k] = v
    }
	for k, v := range fdnp.Annotations {
		pod.Annotations[k] = v
	}


	// Override NodeName
	pod.Spec.NodeName = fdnp.Spec.NodeName

	// Override resources for all containers
	if len(pod.Spec.Containers) > 0 {
		for i := range pod.Spec.Containers {
			// For simplicity, applying the same ResourceRequirements to all containers.
			// A more complex strategy might involve looking at container names or specific annotations.
			pod.Spec.Containers[i].Resources = fdnp.Spec.Resources
		}
	}
	if len(pod.Spec.InitContainers) > 0 {
		for i := range pod.Spec.InitContainers {
			pod.Spec.InitContainers[i].Resources = fdnp.Spec.Resources
		}
	}
	
	// Remove DaemonSet specific fields that are not applicable or managed differently for a single pod
	pod.Spec.Affinity = nil // Affinity for a single pod is usually not copied from a DS template directly.
	pod.Spec.Tolerations = ds.Spec.Template.Spec.Tolerations // Keep tolerations from DS

	// Ensure RestartPolicy is appropriate (DS often uses Always, which is fine for a standalone pod too)
	if pod.Spec.RestartPolicy == "" {
		pod.Spec.RestartPolicy = corev1.RestartPolicyAlways
	}

	return pod, nil
}


// SetupWithManager sets up the controller with the Manager.
func (r *FlexDaemonSetNodePodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&flexdaemonsetsv1alpha1.FlexDaemonSetNodePod{}).
		Owns(&corev1.Pod{}). // Reacts to changes/deletions of pods it creates
		// TODO: Consider watching DaemonSet pods on the target node to detect conflicts more proactively.
		// This would require a more complex Watch setup with custom EnqueueRequestsFromMapFunc.
		// For example:
		// Watches(
		// 	&corev1.Pod{},
		// 	handler.EnqueueRequestsFromMapFunc(r.findFlexDaemonSetNodePodForConflictingPod),
		// 	builder.WithPredicates(predicate.Funcs{
		// 		CreateFunc: func(e event.CreateEvent) bool { ... check if pod is DS owned and on a node managed by an FDNP ... },
		// 		UpdateFunc: func(e event.UpdateEvent) bool { ... },
		// 		DeleteFunc: func(e event.DeleteEvent) bool { return false; }, // Usually FDNP creates pods
		// 	}),
		// ).
		Complete(r)
}
