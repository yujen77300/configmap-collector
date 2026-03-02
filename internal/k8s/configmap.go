package k8s

// ConfigMap list and delete operations.
// All functions accept a kubernetes.Interface so they can be unit-tested with
// fake.NewSimpleClientset() without a live cluster.

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ConfigMapLister lists ConfigMaps in a given namespace.
type ConfigMapLister interface {
	// ListConfigMaps returns ConfigMaps whose name starts with namePrefix.
	// Deprecated: prefer ListAllConfigMaps + FilterConfigMapsByChecksums for
	// multi-service namespaces where a single prefix is insufficient.
	ListConfigMaps(ctx context.Context, namespace, namePrefix string) ([]corev1.ConfigMap, error)

	// ListAllConfigMaps returns every ConfigMap in the namespace with no
	// name-based filtering. Callers use FilterConfigMapsByChecksums to select
	// only those referenced by Argo Rollout ReplicaSets.
	ListAllConfigMaps(ctx context.Context, namespace string) ([]corev1.ConfigMap, error)
}

// ConfigMapDeleter deletes a single ConfigMap by name.
type ConfigMapDeleter interface {
	DeleteConfigMap(ctx context.Context, namespace, name string) error
}

// ConfigMapClient combines listing and deletion into one interface.
type ConfigMapClient interface {
	ConfigMapLister
	ConfigMapDeleter
}

// KubeConfigMapClient is the production implementation backed by a real
// (or fake) kubernetes.Interface.
type KubeConfigMapClient struct {
	client kubernetes.Interface
}

// NewKubeConfigMapClient creates a KubeConfigMapClient wrapping the provided
// kubernetes.Interface. Pass fake.NewSimpleClientset() in tests.
func NewKubeConfigMapClient(client kubernetes.Interface) *KubeConfigMapClient {
	return &KubeConfigMapClient{client: client}
}

// ListConfigMaps returns all ConfigMaps in the given namespace whose name starts
// with namePrefix. It lists all ConfigMaps and filters client-side to avoid
// relying on field selectors that may behave differently across cluster flavors.
//
// Deprecated: prefer ListAllConfigMaps + FilterConfigMapsByChecksums for
// multi-service namespaces where a single prefix is insufficient.
func (k *KubeConfigMapClient) ListConfigMaps(ctx context.Context, namespace, namePrefix string) ([]corev1.ConfigMap, error) {
	list, err := k.client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list configmaps in namespace %q: %w", namespace, err)
	}

	var matched []corev1.ConfigMap
	for _, cm := range list.Items {
		if strings.HasPrefix(cm.Name, namePrefix) {
			matched = append(matched, cm)
		}
	}
	return matched, nil
}

// ListAllConfigMaps returns every ConfigMap in the namespace without any
// name-based filtering. Use FilterConfigMapsByChecksums to narrow the result
// to only those referenced by Argo Rollout ReplicaSets.
func (k *KubeConfigMapClient) ListAllConfigMaps(ctx context.Context, namespace string) ([]corev1.ConfigMap, error) {
	list, err := k.client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list all configmaps in namespace %q: %w", namespace, err)
	}
	return list.Items, nil
}

// FilterConfigMapsByChecksums returns the subset of cms whose name contains at
// least one of the checksums in the provided set. The match is a substring
// check (strings.Contains) so it works regardless of the ConfigMap naming
// convention used by each service (e.g. "xzk0-seat-config-e6120fae",
// "other-svc-config-e6120fae", etc.).
//
// This is the Direction-B approach: instead of filtering by a fixed name prefix,
// we let the checksum set (derived from Rollout ReplicaSet annotations) drive
// which ConfigMaps the GC considers as candidates.
func FilterConfigMapsByChecksums(cms []corev1.ConfigMap, checksums map[string]bool) []corev1.ConfigMap {
	if len(checksums) == 0 {
		return nil
	}
	var matched []corev1.ConfigMap
	for _, cm := range cms {
		for checksum := range checksums {
			if strings.Contains(cm.Name, checksum) {
				matched = append(matched, cm)
				break
			}
		}
	}
	return matched
}

// DeleteConfigMap deletes the named ConfigMap from the given namespace.
// The caller is responsible for enforcing dry-run logic — this function
// always performs a real deletion when invoked.
func (k *KubeConfigMapClient) DeleteConfigMap(ctx context.Context, namespace, name string) error {
	err := k.client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete configmap %q in namespace %q: %w", name, namespace, err)
	}
	return nil
}
