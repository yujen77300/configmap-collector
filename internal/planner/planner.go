package planner

import "time"

type ConfigMapCandidate struct {
	Name              string
	CreationTimestamp time.Time
	Annotations       map[string]string
}

func Plan(
	cms []ConfigMapCandidate,
	inUse map[string]bool,
	keepLast int,
	keepDays int,
	now time.Time,
) []string {
	return []string{}
}
