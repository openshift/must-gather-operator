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
	"k8s.io/apimachinery/pkg/api/errors"
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

	clusterIDSuffix, err := getClusterIDSuffix(ctx, c)
	if err != nil {
		log.V(2).Info("Unable to retrieve cluster ID, continuing without it", "error", err)
	}

	if clusterIDSuffix != "" {
		parts = append(parts, clusterIDSuffix)
	}

	parts = append(parts, now.UTC().Format(timestampFormat))
	parts = append(parts, generateRandomSuffix())

	dirName := strings.Join(parts, ".")
	log.V(1).Info("Generated must-gather directory name", "hasClusterID", clusterIDSuffix != "")

	return dirName
}

// getClusterIDSuffix retrieves the last 12 characters of the cluster ID from the ClusterVersion resource.
// Returns empty string if the ClusterVersion cannot be retrieved or the cluster ID is empty.
func getClusterIDSuffix(ctx context.Context, c client.Client) (string, error) {
	clusterVersion := &configv1.ClusterVersion{}
	err := c.Get(ctx, types.NamespacedName{Name: clusterVersionName}, clusterVersion)
	if err != nil {
		if errors.IsNotFound(err) {
			return "", fmt.Errorf("clusterversion resource not found")
		}
		if errors.IsForbidden(err) {
			return "", fmt.Errorf("insufficient permissions to read clusterversion: %w", err)
		}
		return "", fmt.Errorf("failed to get clusterversion: %w", err)
	}

	clusterID := string(clusterVersion.Spec.ClusterID)
	if clusterID == "" {
		return "", fmt.Errorf("clusterversion.spec.clusterID is empty")
	}

	// Take last 12 characters
	if len(clusterID) <= clusterIDSuffixLength {
		return clusterID, nil
	}

	return clusterID[len(clusterID)-clusterIDSuffixLength:], nil
}

func generateRandomSuffix() string {
	return fmt.Sprintf("%06d", rand.Int63())
}
