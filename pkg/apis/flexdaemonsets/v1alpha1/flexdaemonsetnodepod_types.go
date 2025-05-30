package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FlexDaemonSetNodePodSpec defines the desired state of FlexDaemonSetNodePod
type FlexDaemonSetNodePodSpec struct {
	// DaemonSetName is the name of the target DaemonSet.
	DaemonSetName string `json:"daemonSetName"`

	// DaemonSetNamespace is the namespace of the target DaemonSet.
	DaemonSetNamespace string `json:"daemonSetNamespace"`

	// NodeName is the name of the target Node.
	NodeName string `json:"nodeName"`

	// ObservedDaemonSetTemplateGeneration is the .spec.template.metadata.generation of the DaemonSet when this CR was created/updated.
	// This helps in detecting if the DaemonSet template changed.
	ObservedDaemonSetTemplateGeneration int64 `json:"observedDaemonSetTemplateGeneration"`

	// Resources are the calculated resources to be applied to the pod.
	Resources corev1.ResourceRequirements `json:"resources"`
}

// FlexDaemonSetNodePodStatus defines the observed state of FlexDaemonSetNodePod
type FlexDaemonSetNodePodStatus struct {
	// Phase is the current phase of the FlexDaemonSetNodePod.
	// E.g., "Pending", "Active", "Succeeded", "Failed", "ConflictWithDaemonSet".
	// +optional
	Phase string `json:"phase,omitempty"`

	// Message provides more details about the status.
	// +optional
	Message string `json:"message,omitempty"`

	// ObservedGeneration is the .metadata.generation of the FlexDaemonSetNodePod that was last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of an object's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// FlexDaemonSetNodePod is the Schema for the flexdaemonsetnodepods API
type FlexDaemonSetNodePod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FlexDaemonSetNodePodSpec   `json:"spec,omitempty"`
	Status FlexDaemonSetNodePodStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FlexDaemonSetNodePodList contains a list of FlexDaemonSetNodePod
type FlexDaemonSetNodePodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FlexDaemonSetNodePod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FlexDaemonSetNodePod{}, &FlexDaemonSetNodePodList{})
}
