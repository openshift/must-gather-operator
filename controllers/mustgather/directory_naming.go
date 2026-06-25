/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mustgather

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// clusterVersionName is the name of the singleton ClusterVersion resource
	clusterVersionName = "version"
	// clusterIDSuffixLength is the number of characters to take from the end of the cluster ID
	clusterIDSuffixLength = 12
	// timestampFormat is the Go time layout for the directory name timestamp (matches oc adm must-gather)
	timestampFormat = "20060102T150405Z"
)

// generateMustGatherDirectoryName generates a directory name following the same convention as oc adm must-gather.
// Format: must-gather.local.<cluster-id-suffix>.<timestamp>.<random>
// If cluster ID is unavailable: must-gather.local.<timestamp>.<random>
func generateMustGatherDirectoryName(ctx context.Context, c client.Client, now time.Time) string {
	parts := []string{"must-gather.local"}

	if clusterIDSuffix := getClusterIDSuffix(ctx, c); clusterIDSuffix != "" {
		parts = append(parts, clusterIDSuffix)
	}

	parts = append(parts, now.UTC().Format(timestampFormat))
	parts = append(parts, generateRandomSuffix())

	dirName := strings.Join(parts, ".")
	log.V(1).Info("Generated must-gather directory name", "dirName", dirName)

	return dirName
}

func getClusterIDSuffix(ctx context.Context, c client.Client) string {
	clusterVersion := &configv1.ClusterVersion{}
	if err := c.Get(ctx, types.NamespacedName{Name: clusterVersionName}, clusterVersion); err != nil {
		log.V(2).Info("Unable to retrieve cluster ID for directory name", "error", err)
		return ""
	}

	id := string(clusterVersion.Spec.ClusterID)
	if len(id) > clusterIDSuffixLength {
		return id[len(id)-clusterIDSuffixLength:]
	}
	return id
}

func generateRandomSuffix() string {
	return fmt.Sprintf("%06d", rand.Int63()) //nolint:gosec // not security-sensitive, matches oc adm must-gather convention
}
