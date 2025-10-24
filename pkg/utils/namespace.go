package utils

import (
	"os"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// DefaultOperatorNamespace is the fallback namespace when OPERATOR_NAMESPACE env var is not set
	DefaultOperatorNamespace = "must-gather-operator"
	// OperatorNamespaceEnvVar is the environment variable name for the operator namespace
	OperatorNamespaceEnvVar = "OPERATOR_NAMESPACE"
)

var log = logf.Log.WithName("utils")

// GetOperatorNamespace returns the namespace where the operator is running.
// It first checks the OPERATOR_NAMESPACE environment variable, and falls back
// to the default namespace if not set.
func GetOperatorNamespace() string {
	namespace := os.Getenv(OperatorNamespaceEnvVar)
	if namespace == "" {
		namespace = DefaultOperatorNamespace
		log.Info("OPERATOR_NAMESPACE environment variable not set, using default", "namespace", namespace)
	} else {
		log.Info("Using namespace from environment variable", "namespace", namespace)
	}
	return namespace
}
