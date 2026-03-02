package k8s

// InUseResolver builds the set of checksums that are actively referenced
// by ReplicaSets owned by any Argo Rollout in the given namespace.
//
// Algorithm:
//  1. List all ReplicaSets in the namespace whose ownerReferences point to any
//     Rollout (active RS + history revisions up to revisionHistoryLimit).
//  2. For each RS, extract the checksum/config annotation from the pod template.
//  3. Collect all extracted checksums into a deduplicated set.
//  4. Return the checksum set (e.g. {"e6120fae": true, "b870a608": true}).
//
// Callers use FilterConfigMapsByChecksums to find ConfigMaps whose names contain
// one of these checksums — no fixed name prefix required (Direction B).
//
// If a ReplicaSet has no checksum/config annotation it is silently skipped —
// this can happen for apps that do not use the Helm checksum pattern.

import (
	"context"
	"fmt"
)

// InUseResolver resolves the set of checksums that must not be deleted.
type InUseResolver struct {
	rsClient ReplicaSetLister
}

// NewInUseResolver creates an InUseResolver using the provided ReplicaSetLister.
// No name prefix is needed: the resolver returns raw checksums derived from
// ReplicaSet pod-template annotations ("checksum/config: <hash8>").
func NewInUseResolver(rsClient ReplicaSetLister) *InUseResolver {
	return &InUseResolver{
		rsClient: rsClient,
	}
}

// Resolve returns a set (map[string]bool) of raw checksums that are currently
// referenced by at least one ReplicaSet owned by any Argo Rollout in the namespace.
// Example return value: {"e6120fae": true, "b870a608": true}.
//
// Callers pass this set to FilterConfigMapsByChecksums to select the ConfigMaps
// that are in use, regardless of service name or name prefix.
func (r *InUseResolver) Resolve(ctx context.Context, namespace string) (map[string]bool, error) {
	rsList, err := r.rsClient.ListNamespaceRolloutReplicaSets(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to list replicasets in namespace %q: %w", namespace, err)
	}

	checksums := make(map[string]bool)
	for _, rs := range rsList {
		checksum, ok := ExtractChecksum(rs)
		if !ok {
			// RS has no checksum/config annotation — skip silently.
			continue
		}
		checksums[checksum] = true
	}
	return checksums, nil
}
