package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1"
	"reflect" // For DeepEqual

	"github.com/prakarsh-dt/FlexDaemonsets/pkg/utils"
)

// NodeCoverageReconciler reconciles a Node object by ensuring FlexDaemonSetNodePods
// are created for DaemonSets that should have a pod on that node but don't.
// It primarily watches DaemonSet and Node events.
type NodeCoverageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups=flexdaemonsets.xai,resources=flexdaemonsettemplates,verbs=get;list;watch
//+kubebuilder:rbac:groups=flexdaemonsets.xai,resources=flexdaemonsetnodepods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeCoverageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling NodeCoverage", "request", req.NamespacedName)

	// The request could be for a DaemonSet due to a DS change or a Node change mapped to a DS.
	var currentDS appsv1.DaemonSet
	if err := r.Get(ctx, req.NamespacedName, &currentDS); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("DaemonSet not found, possibly deleted or request was for a Node no longer relevant to any DS.", "request", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get DaemonSet for NodeCoverage reconciliation")
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliation triggered for DaemonSet", "daemonset", currentDS.Name)
	return r.reconcileDaemonSetCoverage(ctx, &currentDS)
}

// reconcileDaemonSetCoverage handles the logic when a DaemonSet event triggers reconciliation.
// It ensures that for each node where the DaemonSet should run, a FlexDaemonSetNodePod exists if the DS pod itself is not there.
func (r *NodeCoverageReconciler) reconcileDaemonSetCoverage(ctx context.Context, ds *appsv1.DaemonSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("daemonset", client.ObjectKeyFromObject(ds).String())

	templateName, ok := ds.Annotations[utils.FlexDaemonsetTemplateAnnotation]
	if !ok {
		logger.Info("DaemonSet does not have the required annotation, skipping", "annotation", utils.FlexDaemonsetTemplateAnnotation)
		// If annotation is removed, existing FDNPs should ideally be cleaned up by their own controller or a cleanup mechanism.
		// This controller focuses on ensuring FDNPs exist when they *should*.
		return ctrl.Result{}, nil
	}

	var fdsTemplate flexdaemonsetsv1alpha1.FlexDaemonsetTemplate
	if err := r.Get(ctx, types.NamespacedName{Name: templateName, Namespace: ds.Namespace}, &fdsTemplate); err != nil {
		logger.Error(err, "Failed to get FlexDaemonsetTemplate", "templateName", templateName)
		// If template is gone, we can't calculate resources. Could requeue or set error status on FDNP.
		return ctrl.Result{}, err
	}

	logger.Info("Processing DaemonSet for node coverage", "templateName", templateName)

	var nodeList corev1.NodeList
	// TODO: Consider adding client.MatchingFields{".spec.schedulerName": ds.Spec.Template.Spec.SchedulerName} if relevant,
	// or other selectors that can be efficiently queried. For now, list all and filter.
	if err := r.List(ctx, &nodeList); err != nil {
		logger.Error(err, "Failed to list nodes")
		return ctrl.Result{}, err
	}

	var dsPods corev1.PodList
	// Using ds.Spec.Selector which should be immutable.
	if err := r.List(ctx, &dsPods, client.InNamespace(ds.Namespace), client.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
		logger.Error(err, "Failed to list pods for DaemonSet", "selector", ds.Spec.Selector.MatchLabels)
		return ctrl.Result{}, err
	}

	podsByNodeName := make(map[string]bool)
	for _, pod := range dsPods.Items {
		if pod.Spec.NodeName != "" {
			podsByNodeName[pod.Spec.NodeName] = true
		}
	}

	// For each node, determine if it's an "uncovered node"
	for i := range nodeList.Items {
		node := &nodeList.Items[i] // Use pointer to allow modifications if needed, and for consistency

		if !r.isNodeSchedulable(node) {
			logger.V(1).Info("Skipping unschedulable node", "nodeName", node.Name)
			continue
		}

		// TODO: Implement more sophisticated check for DaemonSet node affinity/selector matching against the node's labels.
		// This is a complex task involving evaluating node selectors, affinity, and taints/tolerations.
		// For this iteration, we assume if a node is schedulable and doesn't have a DS pod, it's a candidate.
		// A real implementation MUST check if the DaemonSet *would* schedule to this node.

		if _, hasDSPod := podsByNodeName[node.Name]; hasDSPod {
			logger.V(1).Info("Node already has a DaemonSet pod, skipping FDNP creation", "nodeName", node.Name)
			// Potentially, ensure any existing FDNP for this node is deleted if a real DS pod now exists.
			// This might be handled by an FDNP controller or by adding cleanup logic here.
			// For now, focus on creation/update.
			continue
		}

		logger.Info("Node identified as uncovered for DaemonSet", "nodeName", node.Name)

		// --- Resource Calculation ---
		// --- Resource Calculation ---
		calculatedPodResources, errCalc := utils.CalculatePodResources(&fdsTemplate.Spec, node.Status.Allocatable)
		if errCalc != nil {
			logger.Error(errCalc, "Failed to calculate resources for FlexDaemonSetNodePod, skipping FDNP for this node", "nodeName", node.Name, "templateName", fdsTemplate.Name)
			continue // Skip creating/updating FDNP for this node if calculation fails
		}

		fdnpSpecResources := corev1.ResourceRequirements{
			Limits:   calculatedPodResources,
			Requests: calculatedPodResources,
		}
		// --- End Resource Calculation ---

		fdnpName := fmt.Sprintf("%s-%s", ds.Name, node.Name)
		fdnpNamespace := ds.Namespace // FDNP in the same namespace as the DaemonSet

		var existingFdnp flexdaemonsetsv1alpha1.FlexDaemonSetNodePod
		err := r.Get(ctx, types.NamespacedName{Name: fdnpName, Namespace: fdnpNamespace}, &existingFdnp)

		if err != nil {
			if errors.IsNotFound(err) {
				// --- Create FlexDaemonSetNodePod ---
				logger.Info("Creating FlexDaemonSetNodePod for uncovered node", "fdnpName", fdnpName, "nodeName", node.Name)
				newFdnp := &flexdaemonsetsv1alpha1.FlexDaemonSetNodePod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fdnpName,
						Namespace: fdnpNamespace,
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(ds, appsv1.SchemeGroupVersion.WithKind("DaemonSet")),
						},
					},
					Spec: flexdaemonsetsv1alpha1.FlexDaemonSetNodePodSpec{
						DaemonSetName:                   ds.Name,
						DaemonSetNamespace:              ds.Namespace,
						NodeName:                        node.Name,
						ObservedDaemonSetTemplateGeneration: ds.Generation, // Use DS metadata.generation
						Resources:                       fdnpSpecResources,
					},
				}
				if createErr := r.Create(ctx, newFdnp); createErr != nil {
					logger.Error(createErr, "Failed to create FlexDaemonSetNodePod", "fdnpName", fdnpName)
					// Consider requeue: return ctrl.Result{Requeue: true}, nil or return ctrl.Result{}, createErr
					// For now, continue to next node.
				}
				continue // Move to the next node
			} else {
				logger.Error(err, "Failed to get FlexDaemonSetNodePod during create/update check", "fdnpName", fdnpName)
				// Consider requeue or continue
				continue // Move to the next node
			}
		}

		// --- Update FlexDaemonSetNodePod if it exists ---
		// Compare ObservedDaemonSetTemplateGeneration and Resources
		// Note: For ResourceRequirements, reflect.DeepEqual is reliable.
		needsUpdate := false
		if existingFdnp.Spec.ObservedDaemonSetTemplateGeneration != ds.Generation {
			logger.Info("Update needed: ObservedDaemonSetTemplateGeneration changed",
				"fdnpName", existingFdnp.Name,
				"oldGeneration", existingFdnp.Spec.ObservedDaemonSetTemplateGeneration,
				"newGeneration", ds.Generation)
			needsUpdate = true
		}

		if !reflect.DeepEqual(existingFdnp.Spec.Resources, fdnpSpecResources) {
			logger.Info("Update needed: Resources changed",
				"fdnpName", existingFdnp.Name,
				"oldResources", existingFdnp.Spec.Resources,
				"newResources", fdnpSpecResources)
			needsUpdate = true
		}

		if needsUpdate {
			logger.Info("Updating existing FlexDaemonSetNodePod", "fdnpName", existingFdnp.Name)
			updatedFdnp := existingFdnp.DeepCopy() // Work on a copy
			updatedFdnp.Spec.ObservedDaemonSetTemplateGeneration = ds.Generation
			updatedFdnp.Spec.Resources = fdnpSpecResources
			// Ensure owner reference is still correct (though it should be immutable if set correctly at creation)
			updatedFdnp.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(ds, appsv1.SchemeGroupVersion.WithKind("DaemonSet")),
			}

			if updateErr := r.Update(ctx, updatedFdnp); updateErr != nil {
				logger.Error(updateErr, "Failed to update FlexDaemonSetNodePod", "fdnpName", updatedFdnp.Name)
				// Consider requeue
			}
		} else {
			logger.V(1).Info("No update needed for existing FlexDaemonSetNodePod", "fdnpName", existingFdnp.Name)
		}
	} // End loop over nodes

	return ctrl.Result{}, nil
}

// findDaemonSetsForNode is a handler.MapFunc that finds all DaemonSets with the
// FlexDaemonsetTemplateAnnotation and returns reconcile.Requests for them.
// This is used when a Node event occurs, to trigger reconciliation for all relevant DaemonSets.
func (r *NodeCoverageReconciler) findDaemonSetsForNode(ctx context.Context, nodeObj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	node, ok := nodeObj.(*corev1.Node)
	if !ok {
		logger.Error(fmt.Errorf("unexpected type %T for node object", nodeObj), "Node event received for non-Node object")
		return nil
	}

	logger.Info("Node event triggered findDaemonSetsForNode", "nodeName", node.Name)

	var daemonSetList appsv1.DaemonSetList
	if err := r.List(ctx, &daemonSetList); err != nil {
		logger.Error(err, "Failed to list DaemonSets in findDaemonSetsForNode")
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for _, ds := range daemonSetList.Items {
		if _, ok := ds.Annotations[utils.FlexDaemonsetTemplateAnnotation]; ok {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      ds.Name,
					Namespace: ds.Namespace,
				},
			})
		}
	}
	if len(requests) > 0 {
		logger.Info("Mapping Node event to DaemonSet requests", "nodeName", node.Name, "numberOfDaemonSets", len(requests))
	}
	return requests
}

// isNodeSchedulable checks if a node is schedulable.
// This is a basic check and might need to be expanded.
func (r *NodeCoverageReconciler) isNodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			// This is a simplification. A pod might tolerate these taints.
			// For a more accurate check, we'd need to consider DaemonSet's tolerations.
			return false 
		}
	}
	return true
}


// SetupWithManager sets up the controller with the Manager.
func (r *NodeCoverageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index FlexDaemonSetNodePod by NodeName for efficient lookup if needed by other controllers
	// or for more complex logic within this controller (not strictly used by current simple reconcile path).
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &flexdaemonsetsv1alpha1.FlexDaemonSetNodePod{}, ".spec.nodeName", func(rawObj client.Object) []string {
		fdnp := rawObj.(*flexdaemonsetsv1alpha1.FlexDaemonSetNodePod)
		if fdnp.Spec.NodeName == "" {
			return nil
		}
		return []string{fdnp.Spec.NodeName}
	}); err != nil {
		return err
	}

	// Index FlexDaemonSetNodePod by DaemonSet namespaced name for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &flexdaemonsetsv1alpha1.FlexDaemonSetNodePod{}, ".spec.daemonSetNamespacedName", func(rawObj client.Object) []string {
		fdnp := rawObj.(*flexdaemonsetsv1alpha1.FlexDaemonSetNodePod)
		if fdnp.Spec.DaemonSetName == "" || fdnp.Spec.DaemonSetNamespace == "" {
			return nil
		}
		return []string{fdnp.Spec.DaemonSetNamespace + "/" + fdnp.Spec.DaemonSetName}
	}); err != nil {
		return err
	}
	
	// Predicate for DaemonSets: react to create, update (annotation change, spec change affecting template generation).
	// Using AnnotationChangedPredicate for the specific annotation.
	// Also react to spec changes that change metadata.generation (which we use for ObservedDaemonSetTemplateGeneration)
	dsPredicate := predicate.Or(
		predicate.AnnotationChangedPredicate{}, // Changed
		predicate.GenerationChangedPredicate{}, // Reacts if metadata.generation changes (e.g. spec updates)
	)


	return ctrl.NewControllerManagedBy(mgr).
		// Watch DaemonSet resources.
		For(&appsv1.DaemonSet{}, builder.WithPredicates(dsPredicate)).
		// Watch Node resources. Node changes (e.g. labels, schedulability) can affect where DS pods should run.
		// Map Node events to reconciliation requests for all relevant DaemonSets.
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.findDaemonSetsForNode),
			// React to node becoming schedulable/unschedulable, label changes, etc.
			// ResourceVersionChangedPredicate is a bit broad, might need more specific node predicates later.
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		// We are creating FlexDaemonSetNodePod, so Owns could be used if FDNP changes should re-trigger reconciliation of the DS.
		// However, the primary trigger for FDNP creation/update is DS or Node state.
		// If another controller modifies FDNP and NodeCoverageReconciler needs to react, then Owns is appropriate.
		// For now, we explicitly create/update FDNPs. If an FDNP is deleted externally, this reconciler
		// should recreate it on the next DS/Node reconciliation pass.
		// Owns(&flexdaemonsetsv1alpha1.FlexDaemonSetNodePod{}).
		Complete(r)
}

// TODO: Need to create the utils package and CalculatePodResources function.
// For now, resource calculation is a placeholder.
// The OwnerReferences for FDNP should point to the DS.
// The ObservedDaemonSetTemplateGeneration in FDNP spec should be ds.Generation.
// The reconciliation for a DaemonSet should list *all* nodes and check coverage.
// The reconciliation for a Node (via findDaemonSetsForNode) triggers DS reconciliation, which is fine.
// Consider using Server-Side Apply for creating/updating FDNPs for better conflict management.
// client.Patch(ctx, fdnp, client.Apply, client.FieldOwner("node-coverage-controller"))
// Need to ensure the controller has permissions to update DaemonSet status if that becomes necessary. (Not currently updating DS status).
// The current dsPredicate for DaemonSets (AnnotationChangedPredicate and GenerationChangedPredicate) is a good start.
// The Node predicate (ResourceVersionChangedPredicate) is broad; could be refined e.g. specific label changes or status changes.
// The isNodeSchedulable logic is basic; real DS scheduling involves taints/tolerations, node selectors, affinity/anti-affinity.
// This will be refined in subsequent steps.
// The name for FlexDaemonSetNodePod (dsname-nodename) seems reasonable.
// Namespace for FDNP is correctly set to ds.Namespace.
