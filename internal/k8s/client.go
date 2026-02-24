package k8s

// Client factory and Kubernetes client initialization.
// Supports in-cluster config (ServiceAccount) with automatic fallback to
// KUBECONFIG / ~/.kube/config for local development.
// Never hardcodes cluster URLs, contexts, or namespaces — fully cluster-agnostic.

import (
	"fmt"
	"os"

	rolloutclientset "github.com/argoproj/argo-rollouts/pkg/client/clientset/versioned"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients groups the standard Kubernetes clientset and the Argo Rollouts
// clientset so callers can use both through a single struct.
type Clients struct {
	Kube    kubernetes.Interface
	Rollout rolloutclientset.Interface
}

// NewClients creates both a Kubernetes clientset and an Argo Rollouts clientset
// using automatic config detection (in-cluster → KUBECONFIG → ~/.kube/config).
func NewClients() (*Clients, error) {
	cfg, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	rollout, err := rolloutclientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create argo rollouts clientset: %w", err)
	}

	return &Clients{
		Kube:    kube,
		Rollout: rollout,
	}, nil
}

// GetConfig returns a *rest.Config using automatic detection:
//  1. In-cluster config  — when running inside a Kubernetes pod (production).
//  2. KUBECONFIG env var — for local development pointing to any kubeconfig.
//  3. ~/.kube/config     — standard local development fallback.
//
// No cluster URL, context, or namespace is ever hardcoded.
func GetConfig() (*rest.Config, error) {
	// 1. In-cluster config (production: CronJob with ServiceAccount token)
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	// 2. KUBECONFIG env var or default ~/.kube/config (local development)
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile // ~/.kube/config
	}

	cfg, err := clientcmd.BuildConfigFromFlags("" /* masterURL */, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig %q: %w", kubeconfig, err)
	}

	return cfg, nil
}
