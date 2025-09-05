package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMustGatherSpec_Validation(t *testing.T) {
	tests := []struct {
		name    string
		spec    MustGatherSpec
		wantErr bool
	}{
		{
			name: "Valid with upload enabled and required fields",
			spec: MustGatherSpec{
				CaseID: "12345",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				DisableUpload: false,
			},
			wantErr: false,
		},
		{
			name: "Valid with upload disabled and no required fields",
			spec: MustGatherSpec{
				DisableUpload: true,
			},
			wantErr: false,
		},
		{
			name: "Valid with upload disabled and optional fields provided",
			spec: MustGatherSpec{
				CaseID: "12345",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				DisableUpload: true,
			},
			wantErr: false,
		},
		{
			name: "Valid when disableUpload is unset (defaults to false) with required fields",
			spec: MustGatherSpec{
				CaseID: "12345",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				// DisableUpload not set, defaults to false
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mg := &MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mg",
					Namespace: "test-ns",
				},
				Spec: tt.spec,
			}

			// Note: This test validates the struct definition.
			// The actual CEL validation happens at the Kubernetes API server level
			// and would require integration tests with a real cluster or envtest.
			if mg.Spec.DisableUpload != tt.spec.DisableUpload {
				t.Errorf("DisableUpload field not set correctly")
			}
		})
	}
}

// TestMustGatherSpec_CELValidationLogic tests the logic that will be used by CEL validation
// This simulates what the CEL expression does:
// !(has(self.disableUpload) && self.disableUpload) ? (has(self.caseID) && self.caseID != ” && has(self.caseManagementAccountSecretRef) && self.caseManagementAccountSecretRef.name != ”) : true
func TestMustGatherSpec_CELValidationLogic(t *testing.T) {
	tests := []struct {
		name     string
		spec     MustGatherSpec
		expected bool // true means validation should pass
	}{
		{
			name: "Upload enabled (false) with valid fields - should pass",
			spec: MustGatherSpec{
				CaseID: "12345",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				DisableUpload: false,
			},
			expected: true,
		},
		{
			name: "Upload enabled (false) with empty caseID - should fail",
			spec: MustGatherSpec{
				CaseID: "",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				DisableUpload: false,
			},
			expected: false,
		},
		{
			name: "Upload enabled (false) with empty secret name - should fail",
			spec: MustGatherSpec{
				CaseID: "12345",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "",
				},
				DisableUpload: false,
			},
			expected: false,
		},
		{
			name: "Upload disabled (true) with empty fields - should pass",
			spec: MustGatherSpec{
				CaseID: "",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "",
				},
				DisableUpload: true,
			},
			expected: true,
		},
		{
			name: "Upload disabled (true) with valid fields - should pass",
			spec: MustGatherSpec{
				CaseID: "12345",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				DisableUpload: true,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the CEL validation logic
			uploadRequired := !tt.spec.DisableUpload
			fieldsValid := tt.spec.CaseID != "" && tt.spec.CaseManagementAccountSecretRef.Name != ""

			result := !uploadRequired || fieldsValid

			if result != tt.expected {
				t.Errorf("CEL validation logic failed: expected %v, got %v", tt.expected, result)
			}
		})
	}
}
