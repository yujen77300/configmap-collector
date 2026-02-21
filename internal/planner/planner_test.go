package planner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var baseTime = time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)

func TestPlan(t *testing.T) {
	tests := []struct {
		name            string
		configMaps      []ConfigMapCandidate
		inUse           map[string]bool
		keepLast        int
		keepDays        int
		now             time.Time
		expectedDeletes []string
	}{
		{
			name:            "No ConfigMaps",
			configMaps:      []ConfigMapCandidate{},
			inUse:           map[string]bool{},
			keepLast:        5, // revisiotnHistoryLimit + 2
			keepDays:        7,
			now:             baseTime,
			expectedDeletes: []string{},
		},
		{
			name: "keepLast=5 with 5 CMs and 4 in-use: no deletion",
			configMaps: []ConfigMapCandidate{
				{Name: "e6120fae", CreationTimestamp: baseTime.Add(-5 * 24 * time.Hour)},
				{Name: "b870a608", CreationTimestamp: baseTime.Add(-10 * 24 * time.Hour)},
				{Name: "f3bca2cb", CreationTimestamp: baseTime.Add(-15 * 24 * time.Hour)},
				{Name: "d5eb6ebf", CreationTimestamp: baseTime.Add(-20 * 24 * time.Hour)},
				{Name: "da8762a8", CreationTimestamp: baseTime.Add(-30 * 24 * time.Hour)},
			},
			inUse:           map[string]bool{"e6120fae": true, "b870a608": true, "f3bca2cb": true, "d5eb6ebf": true, "da8762a8": false},
			keepLast:        5,
			keepDays:        7,
			now:             baseTime,
			expectedDeletes: []string{},
		},
		{
			name: "keepLast=4 with 5 CMs and 4 in-use: delete oldest one",
			configMaps: []ConfigMapCandidate{
				{Name: "e6120fae", CreationTimestamp: baseTime.Add(-5 * 24 * time.Hour)},
				{Name: "b870a608", CreationTimestamp: baseTime.Add(-10 * 24 * time.Hour)},
				{Name: "f3bca2cb", CreationTimestamp: baseTime.Add(-15 * 24 * time.Hour)},
				{Name: "d5eb6ebf", CreationTimestamp: baseTime.Add(-20 * 24 * time.Hour)},
				{Name: "da8762a8", CreationTimestamp: baseTime.Add(-30 * 24 * time.Hour)},
			},
			inUse:           map[string]bool{"e6120fae": true, "b870a608": true, "f3bca2cb": true, "d5eb6ebf": true, "da8762a8": false},
			keepLast:        4,
			keepDays:        7,
			now:             baseTime,
			expectedDeletes: []string{"da8762a8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Plan(tt.configMaps, tt.inUse, tt.keepLast, tt.keepDays, tt.now)
			assert.ElementsMatch(t, tt.expectedDeletes, result)
		})
	}
}
