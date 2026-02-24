package k8s

// ReplicaSet listing and checksum annotation extraction.
// Identifies all ReplicaSets in a namespace that are owned by an Argo Rollout,
// then extracts the checksum/config annotation from each RS's pod template.
// Only standard apps/v1 and argoproj.io APIs are used — fully cluster-agnostic.

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// AnnotationChecksumConfig is the pod template annotation written by Helm
	// that contains the 8-char SHA256 hash of the mounted ConfigMap's content.
	AnnotationChecksumConfig = "checksum/config"
)

// ReplicaSetLister lists ReplicaSets owned by a named Argo Rollout.
type ReplicaSetLister interface {
	ListRolloutReplicaSets(ctx context.Context, namespace, rolloutName string) ([]appsv1.ReplicaSet, error)
}

// KubeReplicaSetClient is the production implementation backed by a real
// (or fake) kubernetes.Interface.
type KubeReplicaSetClient struct {
	client kubernetes.Interface
}

// NewKubeReplicaSetClient creates a KubeReplicaSetClient wrapping the provided
// kubernetes.Interface. Pass fake.NewSimpleClientset() in tests.
func NewKubeReplicaSetClient(client kubernetes.Interface) *KubeReplicaSetClient {
	return &KubeReplicaSetClient{client: client}
}

// ListRolloutReplicaSets returns all ReplicaSets in the namespace whose
// ownerReferences contain an entry with kind=Rollout and name=rolloutName.
// This covers both the active RS and every history revision retained by the
// Rollout's revisionHistoryLimit.
func (k *KubeReplicaSetClient) ListRolloutReplicaSets(ctx context.Context, namespace, rolloutName string) ([]appsv1.ReplicaSet, error) {
	list, err := k.client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list replicasets in namespace %q: %w", namespace, err)
	}

	var owned []appsv1.ReplicaSet
	for _, rs := range list.Items {
		if isOwnedByRollout(rs, rolloutName) {
			owned = append(owned, rs)
		}
	}
	return owned, nil
}

// isOwnedByRollout returns true when any ownerReference on the ReplicaSet
// has kind="Rollout" and name=rolloutName.
func isOwnedByRollout(rs appsv1.ReplicaSet, rolloutName string) bool {
	for _, ref := range rs.OwnerReferences {
		if ref.Kind == "Rollout" && ref.Name == rolloutName {
			return true
		}
	}
	return false
}

// ExtractChecksum returns the value of the checksum/config annotation from a
// ReplicaSet's pod template. Returns ("", false) when the annotation is absent.
func ExtractChecksum(rs appsv1.ReplicaSet) (string, bool) {
	checksum, ok := rs.Spec.Template.Annotations[AnnotationChecksumConfig]
	return checksum, ok && checksum != ""
}
