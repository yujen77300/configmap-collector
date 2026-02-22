package planner

import (
	"slices"
	"strings"
	"time"
)

type ConfigMapCandidate struct {
	Name              string
	CreationTimestamp time.Time
	Annotations       map[string]string
}

func Plan(cms []ConfigMapCandidate, inUse map[string]bool, keepLast int, keepDays int, now time.Time) []string {
	sorted := make([]ConfigMapCandidate, len(cms))
	copy(sorted, cms)
	slices.SortFunc(sorted, func(a, b ConfigMapCandidate) int {
		return b.CreationTimestamp.Compare(a.CreationTimestamp)
	})

	notInKeepLast := make(map[string]bool)
	for i, cm := range sorted {
		if i >= keepLast {
			notInKeepLast[cm.Name] = true
		}
	}

	keepDuration := time.Duration(keepDays) * 24 * time.Hour

	var toDelete []string
	for _, cm := range sorted {
		if !notInKeepLast[cm.Name] {
			continue
		}
		if inUse[cm.Name] {
			continue
		}
		if cm.Annotations["gc.k8s.io/protect"] == "true" {
			continue
		}
		if strings.Contains(cm.Annotations["argocd.argoproj.io/sync-options"], "PruneLast=true") {
			continue
		}

		// Skip if ConfigMap is too new (not older than keepDays)
		age := now.Sub(cm.CreationTimestamp)
		if age < keepDuration {
			continue
		}
		toDelete = append(toDelete, cm.Name)
	}

	return toDelete
}
