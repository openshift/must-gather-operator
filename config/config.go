package config

import (
	"github.com/openshift/must-gather-operator/pkg/utils"
)

const (
	// OperatorName is the name of this operator
	OperatorName string = "must-gather-operator"

	// MGO is being distributed as part of RVMO now, so use skiprange
	EnableOLMSkipRange string = "true"
)

// GetOperatorNamespace returns the namespace where the operator is running.
// It reads from the OPERATOR_NAMESPACE environment variable with fallback to default.
func GetOperatorNamespace() string {
	return utils.GetOperatorNamespace()
}

// OperatorNamespace returns the namespace where the operator is running.
// Deprecated: Use GetOperatorNamespace() instead for dynamic namespace detection.
var OperatorNamespace = GetOperatorNamespace()
