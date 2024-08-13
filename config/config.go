package config

const (
	// OperatorName is the name of this operator
	OperatorName string = "must-gather-operator"

	// OperatorNamespace
	OperatorNamespace string = "openshift-must-gather-operator"

	// MGO is being distributed as part of RVMO now, so use skiprange
	EnableOLMSkipRange string = "true"
)
