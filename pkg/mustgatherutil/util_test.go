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

package mustgatherutil

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateMustGatherDirectoryName(t *testing.T) {
	patternWithClusterID := regexp.MustCompile(`^must-gather\.local\.[a-zA-Z0-9-]{1,12}\.\d{8}T\d{6}Z\.\d{6,}$`)
	patternWithoutClusterID := regexp.MustCompile(`^must-gather\.local\.\d{8}T\d{6}Z\.\d{6,}$`)

	fixedTime := time.Date(2026, 6, 17, 14, 30, 25, 0, time.UTC)

	tests := []struct {
		name            string
		clusterVersion  *configv1.ClusterVersion
		expectClusterID bool
	}{
		{
			name: "full-length UUID cluster ID — last 12 chars used",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("01234567-89ab-cdef-0123-456789abcdef")},
			},
			expectClusterID: true,
		},
		{
			name: "exactly 12 chars — boundary, full ID used",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("123456789abc")},
			},
			expectClusterID: true,
		},
		{
			name: "13 chars — off-by-one, last 12 taken",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("1234567890abc")},
			},
			expectClusterID: true,
		},
		{
			name:            "ClusterVersion not found — fallback format",
			clusterVersion:  nil,
			expectClusterID: false,
		},
		{
			name: "empty cluster ID — fallback format",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("")},
			},
			expectClusterID: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := configv1.Install(scheme); err != nil {
				t.Fatalf("failed to install configv1 scheme: %v", err)
			}

			var objs []client.Object
			if tt.clusterVersion != nil {
				objs = append(objs, tt.clusterVersion)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			dirName := GenerateMustGatherDirectoryName(context.TODO(), fakeClient, fixedTime)

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
				if len(parts) != 5 {
					t.Fatalf("expected 5 parts with cluster ID, got %d: %s", len(parts), dirName)
				}
			} else {
				if len(parts) != 4 {
					t.Fatalf("expected 4 parts without cluster ID, got %d: %s", len(parts), dirName)
				}
			}

			// Verify prefix
			if parts[0] != "must-gather" || parts[1] != "local" {
				t.Fatalf("expected 'must-gather.local' prefix, got %s.%s", parts[0], parts[1])
			}

			// Verify exact timestamp from fixed time
			expectedTimestamp := "20260617T143025Z"
			timestampIdx := len(parts) - 2
			if parts[timestampIdx] != expectedTimestamp {
				t.Fatalf("expected timestamp %s, got %s", expectedTimestamp, parts[timestampIdx])
			}

			// Verify filesystem safety — no path separators, only safe chars
			filesystemSafe := regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
			if !filesystemSafe.MatchString(dirName) {
				t.Fatalf("directory name contains non-filesystem-safe characters: %s", dirName)
			}
		})
	}
}

func TestGenerateMustGatherDirectoryName_UTCConversion(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatalf("failed to install configv1 scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	estTime := time.Date(2026, 6, 17, 10, 30, 25, 0, time.FixedZone("EST", -5*60*60))
	dirName := GenerateMustGatherDirectoryName(context.TODO(), fakeClient, estTime)
	if !strings.Contains(dirName, "20260617T153025Z") {
		t.Fatalf("expected UTC-converted timestamp 20260617T153025Z in directory name, got %s", dirName)
	}
}

func TestGetClusterIDSuffix_WrongClusterVersionName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatalf("failed to install configv1 scheme: %v", err)
	}

	cv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "not-version"},
		Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("some-cluster-id")},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cv).Build()

	suffix := getClusterIDSuffix(context.TODO(), fakeClient)
	if suffix != "" {
		t.Fatalf("expected empty suffix, got %q", suffix)
	}
}
