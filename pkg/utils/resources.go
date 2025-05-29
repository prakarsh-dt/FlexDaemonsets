package utils

import (
	"fmt"
	// "math" // Not strictly required for math.Max as we are using Quantity.Cmp
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource" // Required for resource.Quantity
	ctrl "sigs.k8s.io/controller-runtime"

	flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1"
)

var log = ctrl.Log.WithName("utils").WithName("resources")

// CalculatePodResources calculates the desired resource requests and limits for a pod's containers
// based on the FlexDaemonsetTemplate and the node's allocatable resources.
// For now, we'll set requests and limits to be the same, as is common for critical workloads like DaemonSets.
func CalculatePodResources(
	templateSpec *flexdaemonsetsv1alpha1.FlexDaemonsetTemplateSpec,
	nodeAllocatable corev1.ResourceList,
) (corev1.ResourceList, error) {

	calculatedResources := corev1.ResourceList{}
	// var err error // This was unused. If it was intended for future use, it should be uncommented.
	// For now, commenting out to fix build error. If any function call here can return an error, it should be handled.

	// Calculate CPU
	cpuAllocatable, ok := nodeAllocatable[corev1.ResourceCPU]
	if !ok {
		log.Info("Node has no allocatable CPU information. Cannot calculate CPU percentage.")
		// Fallback to MinCPU if specified, otherwise, no CPU is requested.
		if templateSpec.MinCPU != "" {
			parsedMinCPU, parseErr := resource.ParseQuantity(templateSpec.MinCPU)
			if parseErr != nil {
				log.Error(parseErr, "Failed to parse MinCPU", "MinCPU", templateSpec.MinCPU)
				return nil, fmt.Errorf("failed to parse MinCPU '%s': %w", templateSpec.MinCPU, parseErr)
			}
			if parsedMinCPU.MilliValue() > 0 { // Only add if MinCPU itself is > 0
				calculatedResources[corev1.ResourceCPU] = parsedMinCPU
			} else {
				log.Info("MinCPU is specified but parses to zero or less, requesting no CPU.", "MinCPU", templateSpec.MinCPU)
			}
		} else {
			log.Info("Node has no allocatable CPU and no MinCPU specified, requesting no CPU.")
		}
	} else {
		// Calculate CPU based on percentage
		cpuPercentageValue := float64(cpuAllocatable.MilliValue()) * (float64(templateSpec.CPUPercentage) / 100.0)
		calculatedCPU := resource.NewMilliQuantity(int64(cpuPercentageValue), resource.DecimalSI)
		
		minCPUQuantitySet := false
		var minCPUQuantity resource.Quantity
		if templateSpec.MinCPU != "" {
			var parseErr error
			minCPUQuantity, parseErr = resource.ParseQuantity(templateSpec.MinCPU)
			if parseErr != nil {
				log.Error(parseErr, "Failed to parse MinCPU", "MinCPU", templateSpec.MinCPU)
				return nil, fmt.Errorf("failed to parse MinCPU '%s': %w", templateSpec.MinCPU, parseErr)
			}
			minCPUQuantitySet = true
		}

		// If MinCPU is specified and calculated CPU is less than MinCPU, use MinCPU.
		if minCPUQuantitySet && calculatedCPU.Cmp(minCPUQuantity) < 0 {
			log.Info("Calculated CPU is less than MinCPU, using MinCPU", "calculatedCPU", calculatedCPU.String(), "minCPU", minCPUQuantity.String())
			calculatedCPU = &minCPUQuantity
		}
		
		// Only add CPU to resources if it's greater than 0.
		if calculatedCPU.MilliValue() > 0 {
		    calculatedResources[corev1.ResourceCPU] = *calculatedCPU
		} else {
		    log.Info("Calculated CPU (after considering MinCPU if any) is zero or less. Requesting no CPU.", "finalCalculatedCPU", calculatedCPU.String())
		}
	}

	// Calculate Memory
	memoryAllocatable, ok := nodeAllocatable[corev1.ResourceMemory]
	if !ok {
		log.Info("Node has no allocatable Memory information. Cannot calculate Memory percentage.")
		if templateSpec.MinMemory != "" {
			parsedMinMemory, parseErr := resource.ParseQuantity(templateSpec.MinMemory)
			if parseErr != nil {
				log.Error(parseErr, "Failed to parse MinMemory", "MinMemory", templateSpec.MinMemory)
				return nil, fmt.Errorf("failed to parse MinMemory '%s': %w", templateSpec.MinMemory, parseErr)
			}
			if parsedMinMemory.Value() > 0 {
				calculatedResources[corev1.ResourceMemory] = parsedMinMemory
			} else {
				log.Info("MinMemory is specified but parses to zero or less, requesting no Memory.", "MinMemory", templateSpec.MinMemory)
			}
		} else {
			log.Info("Node has no allocatable Memory and no MinMemory specified, requesting no Memory.")
		}
	} else {
		memoryPercentageValue := float64(memoryAllocatable.Value()) * (float64(templateSpec.MemoryPercentage) / 100.0)
		calculatedMemory := resource.NewQuantity(int64(memoryPercentageValue), resource.BinarySI)

		minMemoryQuantitySet := false
		var minMemoryQuantity resource.Quantity
		if templateSpec.MinMemory != "" {
			var parseErr error
			minMemoryQuantity, parseErr = resource.ParseQuantity(templateSpec.MinMemory)
			if parseErr != nil {
				log.Error(parseErr, "Failed to parse MinMemory", "MinMemory", templateSpec.MinMemory)
				return nil, fmt.Errorf("failed to parse MinMemory '%s': %w", templateSpec.MinMemory, parseErr)
			}
			minMemoryQuantitySet = true
		}

		if minMemoryQuantitySet && calculatedMemory.Cmp(minMemoryQuantity) < 0 {
			log.Info("Calculated Memory is less than MinMemory, using MinMemory", "calculatedMemory", calculatedMemory.String(), "minMemory", minMemoryQuantity.String())
			calculatedMemory = &minMemoryQuantity
		}

		if calculatedMemory.Value() > 0 {
		    calculatedResources[corev1.ResourceMemory] = *calculatedMemory
		} else {
			log.Info("Calculated Memory (after considering MinMemory if any) is zero or less. Requesting no Memory.", "finalCalculatedMemory", calculatedMemory.String())
		}
	}

	// Calculate Ephemeral Storage
	storageAllocatable, ok := nodeAllocatable[corev1.ResourceEphemeralStorage]
	if !ok {
		log.Info("Node has no allocatable EphemeralStorage information. Cannot calculate Storage percentage.")
		if templateSpec.MinStorage != "" {
			parsedMinStorage, parseErr := resource.ParseQuantity(templateSpec.MinStorage)
			if parseErr != nil {
				log.Error(parseErr, "Failed to parse MinStorage", "MinStorage", templateSpec.MinStorage)
				return nil, fmt.Errorf("failed to parse MinStorage '%s': %w", templateSpec.MinStorage, parseErr)
			}
			if parsedMinStorage.Value() > 0 {
				calculatedResources[corev1.ResourceEphemeralStorage] = parsedMinStorage
			} else {
				log.Info("MinStorage is specified but parses to zero or less, requesting no Storage.", "MinStorage", templateSpec.MinStorage)
			}
		} else {
			log.Info("Node has no allocatable Storage and no MinStorage specified, requesting no Storage.")
		}
	} else {
		storagePercentageValue := float64(storageAllocatable.Value()) * (float64(templateSpec.StoragePercentage) / 100.0)
		calculatedStorage := resource.NewQuantity(int64(storagePercentageValue), resource.BinarySI)

		minStorageQuantitySet := false
		var minStorageQuantity resource.Quantity
		if templateSpec.MinStorage != "" {
			var parseErr error
			minStorageQuantity, parseErr = resource.ParseQuantity(templateSpec.MinStorage)
			if parseErr != nil {
				log.Error(parseErr, "Failed to parse MinStorage", "MinStorage", templateSpec.MinStorage)
				return nil, fmt.Errorf("failed to parse MinStorage '%s': %w", templateSpec.MinStorage, parseErr)
			}
			minStorageQuantitySet = true
		}

		if minStorageQuantitySet && calculatedStorage.Cmp(minStorageQuantity) < 0 {
			log.Info("Calculated Storage is less than MinStorage, using MinStorage", "calculatedStorage", calculatedStorage.String(), "minStorage", minStorageQuantity.String())
			calculatedStorage = &minStorageQuantity
		}
		
		if calculatedStorage.Value() > 0 {
		    calculatedResources[corev1.ResourceEphemeralStorage] = *calculatedStorage
		} else {
			log.Info("Calculated Storage (after considering MinStorage if any) is zero or less. Requesting no Storage.", "finalCalculatedStorage", calculatedStorage.String())
		}
	}
	
	log.Info("Calculated pod resources", "resources", fmt.Sprintf("%v", calculatedResources))
	return calculatedResources, nil
}
