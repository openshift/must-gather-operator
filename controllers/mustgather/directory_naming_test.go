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
	"regexp"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateMustGatherDirectoryName(t *testing.T) {
	// Pattern for directory name with cluster ID:
	// must-gather.local.<cluster-id-up-to-12-chars>.<timestamp-YYYYMMDDTHHMMSSZ>.<6-digit-random>
	// Cluster ID can be 1-12 characters (alphanumeric and hyphens)
	patternWithClusterID := regexp.MustCompile(`^must-gather\.local\.[a-zA-Z0-9-]{1,12}\.\d{8}T\d{6}Z\.\d{6}$`)

	// Pattern for directory name without cluster ID:
	// must-gather.local.<timestamp-YYYYMMDDTHHMMSSZ>.<6-digit-random>
	patternWithoutClusterID := regexp.MustCompile(`^must-gather\.local\.\d{8}T\d{6}Z\.\d{6}$`)

	tests := []struct {
		name               string
		clusterVersion     *configv1.ClusterVersion
		expectClusterID    bool
		expectedErrContains string
	}{
		{
			name: "with valid cluster ID (full length)",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID("01234567-89ab-cdef-0123-456789abcdef"),
				},
			},
			expectClusterID: true,
		},
		{
			name: "with short cluster ID (less than 12 chars)",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID("short123"),
				},
			},
			expectClusterID: true,
		},
		{
			name: "with cluster ID exactly 12 chars",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID("123456789abc"),
				},
			},
			expectClusterID: true,
		},
		{
			name:            "without cluster version (not found)",
			clusterVersion:  nil,
			expectClusterID: false,
		},
		{
			name: "with empty cluster ID",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID(""),
				},
			},
			expectClusterID: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = configv1.Install(scheme)

			var objs []client.Object
			if tt.clusterVersion != nil {
				objs = append(objs, tt.clusterVersion)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			dirName, err := generateMustGatherDirectoryName(context.TODO(), fakeClient)

			if tt.expectedErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.expectedErrContains)
				}
				if !strings.Contains(err.Error(), tt.expectedErrContains) {
					t.Fatalf("expected error containing %q, got %v", tt.expectedErrContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if dirName == "" {
				t.Fatal("directory name should not be empty")
			}

			// Verify format
			if tt.expectClusterID {
				if !patternWithClusterID.MatchString(dirName) {
					t.Fatalf("directory name %q does not match expected pattern with cluster ID", dirName)
				}

				// Verify cluster ID suffix if we have a cluster version
				if tt.clusterVersion != nil && tt.clusterVersion.Spec.ClusterID != "" {
					clusterID := string(tt.clusterVersion.Spec.ClusterID)
					var expectedSuffix string
					if len(clusterID) <= clusterIDSuffixLength {
						expectedSuffix = clusterID
					} else {
						expectedSuffix = clusterID[len(clusterID)-clusterIDSuffixLength:]
					}

					parts := strings.Split(dirName, ".")
					if len(parts) < 4 {
						t.Fatalf("expected at least 4 parts in directory name, got %d: %s", len(parts), dirName)
					}
					actualSuffix := parts[2] // must-gather.local.<suffix>.<timestamp>.<random>

					if actualSuffix != expectedSuffix {
						t.Fatalf("expected cluster ID suffix %q, got %q in directory name %s", expectedSuffix, actualSuffix, dirName)
					}
				}
			} else {
				if !patternWithoutClusterID.MatchString(dirName) {
					t.Fatalf("directory name %q does not match expected pattern without cluster ID", dirName)
				}
			}

			// Verify structure
			parts := strings.Split(dirName, ".")
			if tt.expectClusterID {
				// must-gather.local.<cluster-id>.<timestamp>.<random>
				if len(parts) != 5 {
					t.Fatalf("expected 5 parts with cluster ID, got %d: %s", len(parts), dirName)
				}
			} else {
				// must-gather.local.<timestamp>.<random>
				if len(parts) != 4 {
					t.Fatalf("expected 4 parts without cluster ID, got %d: %s", len(parts), dirName)
				}
			}

			// Verify prefix
			if parts[0] != "must-gather" || parts[1] != "local" {
				t.Fatalf("expected 'must-gather.local' prefix, got %s.%s", parts[0], parts[1])
			}
		})
	}
}

func TestGenerateMustGatherDirectoryName_Uniqueness(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.Install(scheme)

	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: configv1.ClusterID("test-cluster-id-12345"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(clusterVersion).
		Build()

	// Generate multiple directory names and verify they are unique
	names := make(map[string]bool)
	for i := 0; i < 100; i++ {
		dirName, err := generateMustGatherDirectoryName(context.TODO(), fakeClient)
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}

		if names[dirName] {
			t.Fatalf("duplicate directory name generated: %s", dirName)
		}
		names[dirName] = true
	}

	if len(names) != 100 {
		t.Fatalf("expected 100 unique directory names, got %d", len(names))
	}
}

func TestGetClusterIDSuffix(t *testing.T) {
	tests := []struct {
		name           string
		clusterVersion *configv1.ClusterVersion
		expectedSuffix string
		expectError    bool
	}{
		{
			name: "cluster ID longer than 12 chars",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID("01234567-89ab-cdef-0123-456789abcdef"),
				},
			},
			expectedSuffix: "456789abcdef",
			expectError:    false,
		},
		{
			name: "cluster ID exactly 12 chars",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID("123456789abc"),
				},
			},
			expectedSuffix: "123456789abc",
			expectError:    false,
		},
		{
			name: "cluster ID shorter than 12 chars",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID("short"),
				},
			},
			expectedSuffix: "short",
			expectError:    false,
		},
		{
			name: "empty cluster ID",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: configv1.ClusterID(""),
				},
			},
			expectedSuffix: "",
			expectError:    true,
		},
		{
			name:           "cluster version not found",
			clusterVersion: nil,
			expectedSuffix: "",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = configv1.Install(scheme)

			var objs []client.Object
			if tt.clusterVersion != nil {
				objs = append(objs, tt.clusterVersion)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			suffix, err := getClusterIDSuffix(context.TODO(), fakeClient)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if suffix != tt.expectedSuffix {
				t.Fatalf("expected suffix %q, got %q", tt.expectedSuffix, suffix)
			}
		})
	}
}

func TestGenerateRandomSuffix(t *testing.T) {
	// Pattern: 6-digit number (000000-999999)
	pattern := regexp.MustCompile(`^\d{6}$`)

	// Generate multiple suffixes and verify format
	for i := 0; i < 100; i++ {
		suffix := generateRandomSuffix()

		if !pattern.MatchString(suffix) {
			t.Fatalf("random suffix %q does not match expected 6-digit pattern", suffix)
		}

		if len(suffix) != randomIDDigits {
			t.Fatalf("expected suffix length %d, got %d: %s", randomIDDigits, len(suffix), suffix)
		}
	}

	// Verify that multiple calls produce different values (probabilistic test)
	suffixes := make(map[string]bool)
	duplicateCount := 0
	for i := 0; i < 1000; i++ {
		suffix := generateRandomSuffix()
		if suffixes[suffix] {
			duplicateCount++
		}
		suffixes[suffix] = true
	}

	// With 1000000 possible values and 1000 samples, we expect very few duplicates
	// Allow up to 5% duplicates as a generous threshold
	if float64(duplicateCount)/1000.0 > 0.05 {
		t.Fatalf("too many duplicate random suffixes: %d out of 1000 (%.1f%%)", duplicateCount, float64(duplicateCount)/10.0)
	}
}

func TestDirectoryNameFormat(t *testing.T) {
	// This test verifies the exact format matches oc adm must-gather convention
	scheme := runtime.NewScheme()
	_ = configv1.Install(scheme)

	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: configv1.ClusterID("abcdef01-2345-6789-abcd-ef0123456789"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(clusterVersion).
		Build()

	dirName, err := generateMustGatherDirectoryName(context.TODO(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Example expected format: must-gather.local.ef0123456789.20240617T143025Z.042315
	// Verify each component
	parts := strings.Split(dirName, ".")

	if len(parts) != 5 {
		t.Fatalf("expected 5 parts, got %d: %s", len(parts), dirName)
	}

	if parts[0] != "must-gather" {
		t.Fatalf("expected first part 'must-gather', got %s", parts[0])
	}

	if parts[1] != "local" {
		t.Fatalf("expected second part 'local', got %s", parts[1])
	}

	// Cluster ID suffix should be last 12 characters
	expectedSuffix := "ef0123456789"
	if parts[2] != expectedSuffix {
		t.Fatalf("expected cluster ID suffix %s, got %s", expectedSuffix, parts[2])
	}

	// Timestamp should match format YYYYMMDDTHHMMSSZ
	timestampPattern := regexp.MustCompile(`^\d{8}T\d{6}Z$`)
	if !timestampPattern.MatchString(parts[3]) {
		t.Fatalf("timestamp %s does not match expected format YYYYMMDDTHHMMSSZ", parts[3])
	}

	// Random suffix should be 6 digits
	randomPattern := regexp.MustCompile(`^\d{6}$`)
	if !randomPattern.MatchString(parts[4]) {
		t.Fatalf("random suffix %s does not match expected format (6 digits)", parts[4])
	}

	fmt.Printf("Generated directory name: %s\n", dirName)
}
