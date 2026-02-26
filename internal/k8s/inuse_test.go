package k8s

// Unit tests for replicaset.go, rollout.go, and inuse.go.
// No live cluster required — all test doubles use the official fake clientsets:
//   - k8s.io/client-go/kubernetes/fake  for ReplicaSets
//   - github.com/argoproj/argo-rollouts/pkg/client/clientset/versioned/fake  for Rollouts
//
// Scenarios are derived from the real cluster state documented in statusnow.md.

import (
	"context"
	"testing"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	rolloutfake "github.com/argoproj/argo-rollouts/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testRolloutName = "xzk0-seat"
	testRolloutUID  = "855e8f1e-7124-4c1a-9959-1ce7847b780f"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// makeRolloutOwnerRef returns an OwnerReference pointing to an Argo Rollout.
func makeRolloutOwnerRef(rolloutName, uid string) metav1.OwnerReference {
	isController := true
	blockOwner := true
	return metav1.OwnerReference{
		APIVersion:         "argoproj.io/v1alpha1",
		Kind:               "Rollout",
		Name:               rolloutName,
		UID:                k8stypes.UID(uid),
		Controller:         &isController,
		BlockOwnerDeletion: &blockOwner,
	}
}

// makeRS builds a minimal ReplicaSet for tests.
// checksumAnnotation can be "" to simulate a RS without the annotation.
func makeRS(namespace, name, rolloutName, rolloutUID, checksumAnnotation string) appsv1.ReplicaSet {
	rs := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       namespace,
			Name:            name,
			OwnerReferences: []metav1.OwnerReference{makeRolloutOwnerRef(rolloutName, rolloutUID)},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{},
			},
		},
	}
	if checksumAnnotation != "" {
		rs.Spec.Template.Annotations = map[string]string{
			AnnotationChecksumConfig: checksumAnnotation,
		}
	}
	return rs
}

// makeRSNoOwner builds a ReplicaSet that is NOT owned by any Rollout.
func makeRSNoOwner(namespace, name, checksumAnnotation string) appsv1.ReplicaSet {
	rs := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationChecksumConfig: checksumAnnotation,
					},
				},
			},
		},
	}
	return rs
}

// makeRollout builds a minimal Argo Rollout for tests.
func makeRollout(namespace, name string, revisionHistoryLimit *int32) *rolloutsv1alpha1.Rollout {
	return &rolloutsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: rolloutsv1alpha1.RolloutSpec{
			RevisionHistoryLimit: revisionHistoryLimit,
		},
	}
}

// rsToRuntimeObjects converts a slice of ReplicaSets to []runtime.Object.
func rsToRuntimeObjects(rsList []appsv1.ReplicaSet) []runtime.Object {
	objs := make([]runtime.Object, len(rsList))
	for i := range rsList {
		objs[i] = &rsList[i]
	}
	return objs
}

// ─── ExtractChecksum ─────────────────────────────────────────────────────────

func TestExtractChecksum(t *testing.T) {
	tests := []struct {
		name         string
		rs           appsv1.ReplicaSet
		wantChecksum string
		wantOK       bool
	}{
		{
			name:         "returns checksum when annotation is present",
			rs:           makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
			wantChecksum: "e6120fae",
			wantOK:       true,
		},
		{
			name:         "returns false when annotation is absent",
			rs:           makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, ""),
			wantChecksum: "",
			wantOK:       false,
		},
		{
			name: "returns false when pod template has no annotations at all",
			rs: appsv1.ReplicaSet{
				Spec: appsv1.ReplicaSetSpec{
					Template: corev1.PodTemplateSpec{},
				},
			},
			wantChecksum: "",
			wantOK:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotChecksum, gotOK := ExtractChecksum(tc.rs)
			assert.Equal(t, tc.wantChecksum, gotChecksum)
			assert.Equal(t, tc.wantOK, gotOK)
		})
	}
}

// ─── ListRolloutReplicaSets ───────────────────────────────────────────────────

func TestListRolloutReplicaSets(t *testing.T) {
	tests := []struct {
		name          string
		existingRS    []appsv1.ReplicaSet
		rolloutName   string
		expectedNames []string
	}{
		{
			name: "statusnow.md scenario: 4 RS owned by rollout",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"), // rev:26 active
				makeRS(testNamespace, "xzk0-seat-847848bbcf", testRolloutName, testRolloutUID, "b870a608"), // rev:25
				makeRS(testNamespace, "xzk0-seat-6977fddb67", testRolloutName, testRolloutUID, "f3bca2cb"), // rev:24
				makeRS(testNamespace, "xzk0-seat-68b7bd46c8", testRolloutName, testRolloutUID, "d5eb6ebf"), // rev:22
			},
			rolloutName: testRolloutName,
			expectedNames: []string{
				"xzk0-seat-65df947c4c",
				"xzk0-seat-847848bbcf",
				"xzk0-seat-6977fddb67",
				"xzk0-seat-68b7bd46c8",
			},
		},
		{
			name: "filters out RS owned by different rollout",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRS(testNamespace, "other-app-abc123", "other-rollout", "different-uid", "aabbccdd"),
			},
			rolloutName: testRolloutName,
			expectedNames: []string{
				"xzk0-seat-65df947c4c",
			},
		},
		{
			name: "excludes RS with no ownerReferences",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRSNoOwner(testNamespace, "standalone-rs", "e6120fae"),
			},
			rolloutName: testRolloutName,
			expectedNames: []string{
				"xzk0-seat-65df947c4c",
			},
		},
		{
			name:          "returns empty when no RS exist in namespace",
			existingRS:    []appsv1.ReplicaSet{},
			rolloutName:   testRolloutName,
			expectedNames: nil,
		},
		{
			name: "returns empty when no RS is owned by the named rollout",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "other-app-abc123", "other-rollout", "other-uid", "aabbccdd"),
			},
			rolloutName:   testRolloutName,
			expectedNames: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset(rsToRuntimeObjects(tc.existingRS)...)
			rsClient := NewKubeReplicaSetClient(fakeClient)

			got, err := rsClient.ListRolloutReplicaSets(context.Background(), testNamespace, tc.rolloutName)
			require.NoError(t, err)

			gotNames := make([]string, len(got))
			for i, rs := range got {
				gotNames[i] = rs.Name
			}
			assert.ElementsMatch(t, tc.expectedNames, gotNames)
		})
	}
}

// ─── ListNamespaceRolloutReplicaSets ─────────────────────────────────────────

func TestListNamespaceRolloutReplicaSets(t *testing.T) {
	tests := []struct {
		name          string
		existingRS    []appsv1.ReplicaSet
		expectedNames []string
	}{
		{
			name: "returns RS owned by any Rollout regardless of rollout name",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRS(testNamespace, "other-app-abc123", "other-rollout", "other-uid", "aabbccdd"),
			},
			expectedNames: []string{"xzk0-seat-65df947c4c", "other-app-abc123"},
		},
		{
			name: "RS without ownerReferences (no Rollout owner) are excluded",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRSNoOwner(testNamespace, "standalone-rs", "e6120fae"),
			},
			expectedNames: []string{"xzk0-seat-65df947c4c"},
		},
		{
			name:          "empty namespace returns empty slice",
			existingRS:    []appsv1.ReplicaSet{},
			expectedNames: nil,
		},
		{
			name: "multiple rollouts in namespace — all their RS included",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "svc-a-rs1", "svc-a", "uid-a", "aaaaaaaa"),
				makeRS(testNamespace, "svc-a-rs2", "svc-a", "uid-a", "bbbbbbbb"),
				makeRS(testNamespace, "svc-b-rs1", "svc-b", "uid-b", "cccccccc"),
			},
			expectedNames: []string{"svc-a-rs1", "svc-a-rs2", "svc-b-rs1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset(rsToRuntimeObjects(tc.existingRS)...)
			rsClient := NewKubeReplicaSetClient(fakeClient)

			got, err := rsClient.ListNamespaceRolloutReplicaSets(context.Background(), testNamespace)
			require.NoError(t, err)

			gotNames := make([]string, len(got))
			for i, rs := range got {
				gotNames[i] = rs.Name
			}
			assert.ElementsMatch(t, tc.expectedNames, gotNames)
		})
	}
}

// ─── GetRevisionHistoryLimit ──────────────────────────────────────────────────

func TestGetRevisionHistoryLimit(t *testing.T) {
	tests := []struct {
		name        string
		rollout     *rolloutsv1alpha1.Rollout
		rolloutName string
		wantLimit   int
		wantErr     bool
	}{
		{
			name:        "returns configured revisionHistoryLimit (3, from statusnow.md)",
			rollout:     makeRollout(testNamespace, testRolloutName, int32Ptr(3)),
			rolloutName: testRolloutName,
			wantLimit:   3,
			wantErr:     false,
		},
		{
			name:        "returns DefaultRevisionHistoryLimit when field is nil",
			rollout:     makeRollout(testNamespace, testRolloutName, nil),
			rolloutName: testRolloutName,
			wantLimit:   DefaultRevisionHistoryLimit,
			wantErr:     false,
		},
		{
			name:        "returns error when rollout does not exist",
			rollout:     makeRollout(testNamespace, "other-rollout", int32Ptr(3)),
			rolloutName: testRolloutName,
			wantLimit:   0,
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeRolloutClient := rolloutfake.NewSimpleClientset(tc.rollout)
			rolloutClient := NewKubeRolloutClient(fakeRolloutClient)

			got, err := rolloutClient.GetRevisionHistoryLimit(context.Background(), testNamespace, tc.rolloutName)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantLimit, got)
		})
	}
}

// ─── InUseResolver ────────────────────────────────────────────────────────────

func TestInUseResolver_Resolve(t *testing.T) {
	tests := []struct {
		name       string
		existingRS []appsv1.ReplicaSet
		namePrefix string
		wantInUse  map[string]bool
	}{
		{
			name: "statusnow.md: 4 RS → 4 distinct ConfigMaps in-use, da8762a8 is orphan",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"), // rev:26 active
				makeRS(testNamespace, "xzk0-seat-847848bbcf", testRolloutName, testRolloutUID, "b870a608"), // rev:25
				makeRS(testNamespace, "xzk0-seat-6977fddb67", testRolloutName, testRolloutUID, "f3bca2cb"), // rev:24
				makeRS(testNamespace, "xzk0-seat-68b7bd46c8", testRolloutName, testRolloutUID, "d5eb6ebf"), // rev:22
			},
			namePrefix: testPrefix,
			wantInUse: map[string]bool{
				"xzk0-seat-config-e6120fae": true,
				"xzk0-seat-config-b870a608": true,
				"xzk0-seat-config-f3bca2cb": true,
				"xzk0-seat-config-d5eb6ebf": true,
				// da8762a8 is NOT listed → orphan → eligible for deletion
			},
		},
		{
			name: "RS without checksum annotation is silently skipped",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRS(testNamespace, "xzk0-seat-847848bbcf", testRolloutName, testRolloutUID, ""), // no annotation
			},
			namePrefix: testPrefix,
			wantInUse: map[string]bool{
				"xzk0-seat-config-e6120fae": true,
				// xzk0-seat-847848bbcf is skipped, no ConfigMap added
			},
		},
		{
			name:       "empty namespace: returns empty in-use set",
			existingRS: []appsv1.ReplicaSet{},
			namePrefix: testPrefix,
			wantInUse:  map[string]bool{},
		},
		{
			name: "RS owned by different rollout is also included (namespace-wide scan)",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRS(testNamespace, "other-app-abc123", "other-rollout", "other-uid", "deadbeef"),
			},
			namePrefix: testPrefix,
			// other-rollout's RS checksum won't match the prefix xzk0-seat-config-,
			// but it is still scanned. Only e6120fae resolves to a valid CM name.
			wantInUse: map[string]bool{
				"xzk0-seat-config-e6120fae": true,
				"xzk0-seat-config-deadbeef": true,
			},
		},
		{
			name: "duplicate checksum across RS is deduplicated",
			existingRS: []appsv1.ReplicaSet{
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRS(testNamespace, "xzk0-seat-aabbccdd00", testRolloutName, testRolloutUID, "e6120fae"), // same checksum
			},
			namePrefix: testPrefix,
			wantInUse: map[string]bool{
				"xzk0-seat-config-e6120fae": true,
			},
		},
		{
			name: "multiple Rollouts in namespace — all RS checksums included in inUse",
			existingRS: []appsv1.ReplicaSet{
				// Rollout A: xzk0-seat
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				makeRS(testNamespace, "xzk0-seat-847848bbcf", testRolloutName, testRolloutUID, "b870a608"),
				// Rollout B: other-app (different service, same namespace)
				makeRS(testNamespace, "other-app-rs1", "other-app", "other-uid", "aaaabbbb"),
			},
			namePrefix: testPrefix,
			wantInUse: map[string]bool{
				// all three RS checksums are included regardless of rollout name
				"xzk0-seat-config-e6120fae": true,
				"xzk0-seat-config-b870a608": true,
				"xzk0-seat-config-aaaabbbb": true,
			},
		},
		{
			name: "RS owned by Deployment (not Rollout) is excluded from inUse",
			existingRS: []appsv1.ReplicaSet{
				// Owned by Rollout — should be included
				makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
				// Owned by Deployment — should be excluded
				makeRSNoOwner(testNamespace, "deploy-managed-rs", "cafecafe"),
			},
			namePrefix: testPrefix,
			wantInUse: map[string]bool{
				"xzk0-seat-config-e6120fae": true,
				// cafecafe is NOT included — its RS has no Rollout ownerRef
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset(rsToRuntimeObjects(tc.existingRS)...)
			rsClient := NewKubeReplicaSetClient(fakeClient)
			resolver := NewInUseResolver(rsClient, tc.namePrefix)

			got, err := resolver.Resolve(context.Background(), testNamespace)
			require.NoError(t, err)
			assert.Equal(t, tc.wantInUse, got)
		})
	}
}

// TestInUseResolver_OrphanScenario verifies the key business scenario from
// statusnow.md: exactly one ConfigMap (da8762a8) is orphaned and should be
// eligible for deletion when combined with the planner.
func TestInUseResolver_OrphanScenario(t *testing.T) {
	// 5 ConfigMaps exist, but only 4 are referenced by ReplicaSets.
	rsList := []appsv1.ReplicaSet{
		makeRS(testNamespace, "xzk0-seat-65df947c4c", testRolloutName, testRolloutUID, "e6120fae"),
		makeRS(testNamespace, "xzk0-seat-847848bbcf", testRolloutName, testRolloutUID, "b870a608"),
		makeRS(testNamespace, "xzk0-seat-6977fddb67", testRolloutName, testRolloutUID, "f3bca2cb"),
		makeRS(testNamespace, "xzk0-seat-68b7bd46c8", testRolloutName, testRolloutUID, "d5eb6ebf"),
	}

	fakeClient := fake.NewSimpleClientset(rsToRuntimeObjects(rsList)...)
	rsClient := NewKubeReplicaSetClient(fakeClient)
	resolver := NewInUseResolver(rsClient, testPrefix)

	inUse, err := resolver.Resolve(context.Background(), testNamespace)
	require.NoError(t, err)

	// da8762a8 is the orphaned ConfigMap — must NOT be in the in-use set.
	assert.False(t, inUse["xzk0-seat-config-da8762a8"],
		"da8762a8 should not be in-use (orphaned ConfigMap)")

	// All 4 referenced ConfigMaps must be in the in-use set.
	assert.True(t, inUse["xzk0-seat-config-e6120fae"])
	assert.True(t, inUse["xzk0-seat-config-b870a608"])
	assert.True(t, inUse["xzk0-seat-config-f3bca2cb"])
	assert.True(t, inUse["xzk0-seat-config-d5eb6ebf"])
	assert.Len(t, inUse, 4)
}

// ─── Interface compliance ──────────────────────────────────────────────────────

// TestKubeReplicaSetClient_ImplementsInterface is a compile-time check.
func TestKubeReplicaSetClient_ImplementsInterface(t *testing.T) {
	var _ ReplicaSetLister = (*KubeReplicaSetClient)(nil)
}

// TestKubeRolloutClient_ImplementsInterface is a compile-time check.
func TestKubeRolloutClient_ImplementsInterface(t *testing.T) {
	var _ RolloutGetter = (*KubeRolloutClient)(nil)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// int32Ptr returns a pointer to an int32 value, for use in Rollout specs.
func int32Ptr(v int32) *int32 { return &v }
