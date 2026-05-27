// Package kube builds a kubernetes.Interface that works both inside a Pod
// (via the projected service account token) and outside (via the user's
// kubeconfig). Services that don't need a full controller-runtime manager
// — like the api streaming pod logs — use this lighter client factory.
package kube

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// New returns a configured client. In-cluster config is preferred; the
// loader falls back to KUBECONFIG / ~/.kube/config / current context.
func New() (kubernetes.Interface, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("new clientset: %w", err)
	}
	return cli, nil
}

func loadConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	return cfg, nil
}
