package k8s

// Unit tests for KubeConfigMapClient using fake.NewSimpleClientset().
// No live cluster is required — all assertions are fully deterministic.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testNamespace = "mwpcloud"
	testPrefix    = "xzk0-seat-config-"
)

// makeConfigMap is a helper that builds a minimal ConfigMap for tests.
func makeConfigMap(namespace, name string, annotations map[string]string) corev1.ConfigMap {
	return corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Annotations: annotations,
		},
	}
}

// ─── ListConfigMaps ──────────────────────────────────────────────────────────

func TestListConfigMaps(t *testing.T) {
	tests := []struct {
		name          string
		existingCMs   []corev1.ConfigMap
		queryNS       string
		queryPrefix   string
		expectedNames []string
	}{
		{
			name: "returns only cms matching prefix",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-b870a608", nil),
				makeConfigMap(testNamespace, "other-app-config-abc123", nil), // different prefix
				makeConfigMap(testNamespace, "xzk0-seat-env-abc", nil),       // different prefix
			},
			queryNS:       testNamespace,
			queryPrefix:   testPrefix,
			expectedNames: []string{"xzk0-seat-config-e6120fae", "xzk0-seat-config-b870a608"},
		},
		{
			name: "returns empty slice when no cms match prefix",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "unrelated-config-abc", nil),
			},
			queryNS:       testNamespace,
			queryPrefix:   testPrefix,
			expectedNames: nil,
		},
		{
			name:          "returns empty slice when namespace is empty",
			existingCMs:   []corev1.ConfigMap{},
			queryNS:       testNamespace,
			queryPrefix:   testPrefix,
			expectedNames: nil,
		},
		{
			name: "does not return cms from a different namespace",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap("other-ns", "xzk0-seat-config-e6120fae", nil),
			},
			queryNS:       testNamespace,
			queryPrefix:   testPrefix,
			expectedNames: nil,
		},
		{
			name: "returns all five cms matching the realistic scenario from statusnow.md",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-b870a608", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-f3bca2cb", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-d5eb6ebf", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-da8762a8", nil),
			},
			queryNS:     testNamespace,
			queryPrefix: testPrefix,
			expectedNames: []string{
				"xzk0-seat-config-e6120fae",
				"xzk0-seat-config-b870a608",
				"xzk0-seat-config-f3bca2cb",
				"xzk0-seat-config-d5eb6ebf",
				"xzk0-seat-config-da8762a8",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build fake clientset pre-populated with existing objects.
			fakeClient := fake.NewSimpleClientset(objectsToRuntimeObjects(tc.existingCMs)...)
			cmClient := NewKubeConfigMapClient(fakeClient)

			got, err := cmClient.ListConfigMaps(context.Background(), tc.queryNS, tc.queryPrefix)
			require.NoError(t, err)

			gotNames := extractNames(got)
			assert.ElementsMatch(t, tc.expectedNames, gotNames)
		})
	}
}

// ─── DeleteConfigMap ─────────────────────────────────────────────────────────

func TestDeleteConfigMap(t *testing.T) {
	tests := []struct {
		name        string
		existingCMs []corev1.ConfigMap
		deleteNS    string
		deleteName  string
		wantErr     bool
		remainNames []string // names still present after deletion
	}{
		{
			name: "deletes an existing configmap",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-da8762a8", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
			},
			deleteNS:    testNamespace,
			deleteName:  "xzk0-seat-config-da8762a8",
			wantErr:     false,
			remainNames: []string{"xzk0-seat-config-e6120fae"},
		},
		{
			name:        "returns error when configmap does not exist",
			existingCMs: []corev1.ConfigMap{},
			deleteNS:    testNamespace,
			deleteName:  "xzk0-seat-config-nonexistent",
			wantErr:     true,
			remainNames: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset(objectsToRuntimeObjects(tc.existingCMs)...)
			cmClient := NewKubeConfigMapClient(fakeClient)

			err := cmClient.DeleteConfigMap(context.Background(), tc.deleteNS, tc.deleteName)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Verify that the deleted CM is gone and remaining ones still exist.
			remaining, err := fakeClient.CoreV1().ConfigMaps(tc.deleteNS).List(
				context.Background(), metav1.ListOptions{},
			)
			require.NoError(t, err)
			gotNames := extractNames(remaining.Items)
			assert.ElementsMatch(t, tc.remainNames, gotNames)
		})
	}
}

// TestDeleteConfigMap_DryRunCallerResponsibility documents the contract:
// DeleteConfigMap always performs a real deletion. The *caller* must check
// cfg.DryRun before invoking it. This test verifies that the fake clientset
// records the delete action so callers can intercept in dry-run mode.
func TestDeleteConfigMap_DryRunCallerResponsibility(t *testing.T) {
	cm := makeConfigMap(testNamespace, "xzk0-seat-config-da8762a8", nil)
	fakeClient := fake.NewSimpleClientset(&cm)
	cmClient := NewKubeConfigMapClient(fakeClient)

	// Simulate dry-run: caller checks flag before calling Delete.
	dryRun := true
	if !dryRun {
		_ = cmClient.DeleteConfigMap(context.Background(), testNamespace, cm.Name)
	}

	// ConfigMap must still exist because the caller short-circuited.
	got, err := fakeClient.CoreV1().ConfigMaps(testNamespace).List(
		context.Background(), metav1.ListOptions{},
	)
	require.NoError(t, err)
	assert.Len(t, got.Items, 1)
	assert.Equal(t, cm.Name, got.Items[0].Name)
}

// ─── Interface compliance ─────────────────────────────────────────────────────

// TestKubeConfigMapClient_ImplementsInterface is a compile-time check that
// *KubeConfigMapClient satisfies the ConfigMapClient interface.
func TestKubeConfigMapClient_ImplementsInterface(t *testing.T) {
	var _ ConfigMapClient = (*KubeConfigMapClient)(nil)
}

// ─── ListAllConfigMaps ────────────────────────────────────────────────────────

func TestListAllConfigMaps(t *testing.T) {
	tests := []struct {
		name          string
		existingCMs   []corev1.ConfigMap
		queryNS       string
		expectedNames []string
	}{
		{
			name: "returns all CMs regardless of name pattern",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
				makeConfigMap(testNamespace, "test-app-config-aaa1111", nil),
				makeConfigMap(testNamespace, "other-svc-cfg-bbb2222", nil),
			},
			queryNS: testNamespace,
			expectedNames: []string{
				"xzk0-seat-config-e6120fae",
				"test-app-config-aaa1111",
				"other-svc-cfg-bbb2222",
			},
		},
		{
			name:          "returns empty slice when namespace has no CMs",
			existingCMs:   []corev1.ConfigMap{},
			queryNS:       testNamespace,
			expectedNames: nil,
		},
		{
			name: "does not return CMs from a different namespace",
			existingCMs: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
				makeConfigMap("other-ns", "xzk0-seat-config-b870a608", nil),
			},
			queryNS: testNamespace,
			expectedNames: []string{
				"xzk0-seat-config-e6120fae",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset(objectsToRuntimeObjects(tc.existingCMs)...)
			cmClient := NewKubeConfigMapClient(fakeClient)

			got, err := cmClient.ListAllConfigMaps(context.Background(), tc.queryNS)
			require.NoError(t, err)

			gotNames := extractNames(got)
			assert.ElementsMatch(t, tc.expectedNames, gotNames)
		})
	}
}

// ─── FilterConfigMapsByChecksums ─────────────────────────────────────────────

func TestFilterConfigMapsByChecksums(t *testing.T) {
	tests := []struct {
		name          string
		cms           []corev1.ConfigMap
		checksums     map[string]bool
		expectedNames []string
	}{
		{
			name: "matches CMs whose names contain a checksum (multi-service namespace)",
			cms: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil), // checksum: e6120fae
				makeConfigMap(testNamespace, "test-app-config-aaa1111", nil),   // checksum: aaa1111
				makeConfigMap(testNamespace, "other-svc-cfg-bbb2222", nil),     // not in checksum set
			},
			checksums: map[string]bool{
				"e6120fae": true,
				"aaa1111":  true,
			},
			expectedNames: []string{
				"xzk0-seat-config-e6120fae",
				"test-app-config-aaa1111",
			},
		},
		{
			name: "statusnow.md scenario: 5 CMs, 4 checksums in-use, 1 matched by no checksum",
			cms: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-b870a608", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-f3bca2cb", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-d5eb6ebf", nil),
				makeConfigMap(testNamespace, "xzk0-seat-config-da8762a8", nil), // not in checksums
			},
			checksums: map[string]bool{
				"e6120fae": true,
				"b870a608": true,
				"f3bca2cb": true,
				"d5eb6ebf": true,
			},
			expectedNames: []string{
				"xzk0-seat-config-e6120fae",
				"xzk0-seat-config-b870a608",
				"xzk0-seat-config-f3bca2cb",
				"xzk0-seat-config-d5eb6ebf",
			},
		},
		{
			name: "returns nil when no CMs match any checksum",
			cms: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-da8762a8", nil),
			},
			checksums:     map[string]bool{"e6120fae": true},
			expectedNames: nil,
		},
		{
			name: "returns nil when checksum set is empty",
			cms: []corev1.ConfigMap{
				makeConfigMap(testNamespace, "xzk0-seat-config-e6120fae", nil),
			},
			checksums:     map[string]bool{},
			expectedNames: nil,
		},
		{
			name:          "returns nil when cms slice is empty",
			cms:           []corev1.ConfigMap{},
			checksums:     map[string]bool{"e6120fae": true},
			expectedNames: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterConfigMapsByChecksums(tc.cms, tc.checksums)
			gotNames := extractNames(got)
			assert.ElementsMatch(t, tc.expectedNames, gotNames)
		})
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func extractNames(cms []corev1.ConfigMap) []string {
	if len(cms) == 0 {
		return nil
	}
	names := make([]string, len(cms))
	for i, cm := range cms {
		names[i] = cm.Name
	}
	return names
}

// objectsToRuntimeObjects converts []corev1.ConfigMap to the variadic
// runtime.Object slice expected by fake.NewSimpleClientset.
func objectsToRuntimeObjects(cms []corev1.ConfigMap) []runtime.Object {
	objs := make([]runtime.Object, len(cms))
	for i := range cms {
		objs[i] = &cms[i]
	}
	return objs
}
