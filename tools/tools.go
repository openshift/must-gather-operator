// Place any runtime dependencies as imports in this file.
// Go modules will be forced to download and install them.
package tools

import (
	// kube-openapi: openapi-gen binary is used in `make generate`
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
	// controller-gen: binary is used in API generations
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
