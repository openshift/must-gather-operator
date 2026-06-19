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
			name: "short cluster ID (8 chars) — full ID used",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("short123")},
			},
			expectClusterID: true,
		},
		{
			name: "1-char cluster ID — full ID used",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("x")},
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
			name: "100+ chars — only last 12 used",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789LAST12CHARSX")},
			},
			expectClusterID: true,
		},
		{
			name: "cluster ID with only hyphens and digits",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("1234-5678-9abc-def0")},
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

			dirName := generateMustGatherDirectoryName(context.TODO(), fakeClient, fixedTime)

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

func TestGenerateMustGatherDirectoryName_Uniqueness(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatalf("failed to install configv1 scheme: %v", err)
	}

	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Spec:       configv1.ClusterVersionSpec{ClusterID: configv1.ClusterID("test-cluster-id-12345")},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(clusterVersion).
		Build()

	names := make(map[string]bool)
	now := time.Now()
	for i := 0; i < 100; i++ {
		dirName := generateMustGatherDirectoryName(context.TODO(), fakeClient, now)
		if names[dirName] {
			t.Fatalf("duplicate directory name generated: %s", dirName)
		}
		names[dirName] = true
	}

	if len(names) != 100 {
		t.Fatalf("expected 100 unique directory names, got %d", len(names))
	}

	// Verify random suffix is zero-padded to at least 6 digits
	for name := range names {
		parts := strings.Split(name, ".")
		suffix := parts[len(parts)-1]
		if len(suffix) < 6 {
			t.Fatalf("random suffix should be at least 6 digits (zero-padded), got %q", suffix)
		}
		for _, c := range suffix {
			if c < '0' || c > '9' {
				t.Fatalf("random suffix should contain only digits, got %q", suffix)
			}
		}
	}
}

func TestGenerateMustGatherDirectoryName_UTCConversion(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatalf("failed to install configv1 scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	tests := []struct {
		name              string
		inputTime         time.Time
		expectedTimestamp string
	}{
		{
			name:              "non-UTC timezone converted to UTC",
			inputTime:         time.Date(2026, 6, 17, 10, 30, 25, 0, time.FixedZone("EST", -5*60*60)),
			expectedTimestamp: "20260617T153025Z",
		},
		{
			name:              "midnight Jan 1 — no zero-stripping",
			inputTime:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			expectedTimestamp: "20260101T000000Z",
		},
		{
			name:              "end of year Dec 31 23:59:59",
			inputTime:         time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
			expectedTimestamp: "20261231T235959Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirName := generateMustGatherDirectoryName(context.TODO(), fakeClient, tt.inputTime)
			if !strings.Contains(dirName, tt.expectedTimestamp) {
				t.Fatalf("expected timestamp %s in directory name, got %s", tt.expectedTimestamp, dirName)
			}
		})
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

	suffix, err := getClusterIDSuffix(context.TODO(), fakeClient)
	if err == nil {
		t.Fatal("expected error when ClusterVersion has wrong name, got none")
	}
	if suffix != "" {
		t.Fatalf("expected empty suffix, got %q", suffix)
	}
}
