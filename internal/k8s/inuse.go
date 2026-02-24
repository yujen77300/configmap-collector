package k8s

// InUseResolver builds the set of ConfigMap names that are actively referenced
// by ReplicaSets owned by a given Argo Rollout.
//
// Algorithm:
//  1. List all ReplicaSets in the namespace whose ownerReferences point to the
//     named Rollout (active RS + history revisions up to revisionHistoryLimit).
//  2. For each RS, extract the checksum/config annotation from the pod template.
//  3. Reconstruct the ConfigMap name as "{namePrefix}{checksum}".
//  4. Return a deduplicated set of in-use ConfigMap names.
//
// If a ReplicaSet has no checksum/config annotation it is silently skipped —
// this can happen for apps that do not use the Helm checksum pattern.

import (
	"context"
	"fmt"
)

// InUseResolver resolves the set of ConfigMap names that must not be deleted.
type InUseResolver struct {
	rsClient   ReplicaSetLister
	namePrefix string
}

// NewInUseResolver creates an InUseResolver using the provided ReplicaSetLister.
// namePrefix must match the ConfigMap naming pattern, e.g. "xzk0-seat-config-".
func NewInUseResolver(rsClient ReplicaSetLister, namePrefix string) *InUseResolver {
	return &InUseResolver{
		rsClient:   rsClient,
		namePrefix: namePrefix,
	}
}

// Resolve returns a set (map[string]bool) of ConfigMap names that are currently
// referenced by at least one ReplicaSet owned by rolloutName.
func (r *InUseResolver) Resolve(ctx context.Context, namespace, rolloutName string) (map[string]bool, error) {
	rsList, err := r.rsClient.ListRolloutReplicaSets(ctx, namespace, rolloutName)
	if err != nil {
		return nil, fmt.Errorf("failed to list replicasets for rollout %q: %w", rolloutName, err)
	}

	inUse := make(map[string]bool)
	for _, rs := range rsList {
		checksum, ok := ExtractChecksum(rs)
		if !ok {
			// RS has no checksum/config annotation — skip silently.
			continue
		}
		cmName := r.namePrefix + checksum
		inUse[cmName] = true
	}
	return inUse, nil
}
