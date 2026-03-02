package k8s

// Rollout retrieval and revisionHistoryLimit extraction.
// Uses the Argo Rollouts clientset (argoproj.io/v1alpha1) — the only
// non-core API this application depends on, and it is cluster-agnostic
// as long as Argo Rollouts is installed in the target cluster.

import (
	"context"
	"fmt"

	rolloutclientset "github.com/argoproj/argo-rollouts/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultRevisionHistoryLimit is the Argo Rollouts default when the field is
// unset in the Rollout spec. Mirrors the upstream default of 10.
const DefaultRevisionHistoryLimit = 10

// RolloutGetter retrieves a single Rollout by name.
type RolloutGetter interface {
	GetRevisionHistoryLimit(ctx context.Context, namespace, rolloutName string) (int, error)
}

// RolloutLister lists all Rollout names in a namespace.
type RolloutLister interface {
	ListRolloutNames(ctx context.Context, namespace string) ([]string, error)
}

// KubeRolloutClient is the production implementation backed by the Argo
// Rollouts clientset. Pass a fake rollout clientset in unit tests.
type KubeRolloutClient struct {
	client rolloutclientset.Interface
}

// NewKubeRolloutClient creates a KubeRolloutClient wrapping the provided
// Argo Rollouts clientset.Interface.
func NewKubeRolloutClient(client rolloutclientset.Interface) *KubeRolloutClient {
	return &KubeRolloutClient{client: client}
}

// ListRolloutNames returns the names of all Argo Rollouts in the given namespace.
// This is used to auto-derive ConfigMap name prefixes without requiring
// manual configuration — each Rollout named "foo" manages ConfigMaps with
// prefix "foo-config-".
func (k *KubeRolloutClient) ListRolloutNames(ctx context.Context, namespace string) ([]string, error) {
	list, err := k.client.ArgoprojV1alpha1().Rollouts(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list rollouts in namespace %q: %w", namespace, err)
	}
	names := make([]string, 0, len(list.Items))
	for _, r := range list.Items {
		names = append(names, r.Name)
	}
	return names, nil
}

// GetRevisionHistoryLimit returns the revisionHistoryLimit from the named
// Rollout's spec. If the field is nil (unset), DefaultRevisionHistoryLimit is
// returned so callers always receive a usable integer.
func (k *KubeRolloutClient) GetRevisionHistoryLimit(ctx context.Context, namespace, rolloutName string) (int, error) {
	rollout, err := k.client.ArgoprojV1alpha1().Rollouts(namespace).Get(ctx, rolloutName, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get rollout %q in namespace %q: %w", rolloutName, namespace, err)
	}

	if rollout.Spec.RevisionHistoryLimit == nil {
		return DefaultRevisionHistoryLimit, nil
	}
	return int(*rollout.Spec.RevisionHistoryLimit), nil
}
