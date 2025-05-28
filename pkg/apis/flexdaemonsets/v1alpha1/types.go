package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FlexDaemonsetTemplateSpec defines the desired state of FlexDaemonsetTemplate
type FlexDaemonsetTemplateSpec struct {
	// CPUPercentage is the percentage of CPU to allocate from the node's allocatable CPU.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	CPUPercentage int32 `json:"cpuPercentage"`

	// MemoryPercentage is the percentage of Memory to allocate from the node's allocatable memory.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MemoryPercentage int32 `json:"memoryPercentage"`

	// StoragePercentage is the percentage of ephemeral-storage to allocate from the node's allocatable ephemeral-storage.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	StoragePercentage int32 `json:"storagePercentage"`

	// MinCPU specifies the minimum absolute CPU request in milliCPU (e.g., "100m").
	// +optional
	MinCPU string `json:"minCPU,omitempty"`

	// MinMemory specifies the minimum absolute memory request (e.g., "64Mi").
	// +optional
	MinMemory string `json:"minMemory,omitempty"`

	// MinStorage specifies the minimum absolute ephemeral-storage request (e.g., "1Gi").
	// +optional
	MinStorage string `json:"minStorage,omitempty"`
}

// FlexDaemonsetTemplateStatus defines the observed state of FlexDaemonsetTemplate
// This can be used for status reporting in the future, but is not strictly needed for the webhook.
type FlexDaemonsetTemplateStatus struct {
	// Conditions represent the latest available observations of a FlexDaemonsetTemplate's current state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=fdt
// +kubebuilder:subresource:status
// FlexDaemonsetTemplate is the Schema for the flexdaemonsettemplates API
type FlexDaemonsetTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FlexDaemonsetTemplateSpec   `json:"spec,omitempty"`
	Status FlexDaemonsetTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// FlexDaemonsetTemplateList contains a list of FlexDaemonsetTemplate
type FlexDaemonsetTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FlexDaemonsetTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FlexDaemonsetTemplate{}, &FlexDaemonsetTemplateList{})
}
