package utils

import (
	"os"
	"testing"
)

func TestGetOperatorNamespace(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected string
		setupEnv bool
		clearEnv bool
	}{
		{
			name:     "uses default namespace when env var not set",
			expected: DefaultOperatorNamespace,
			clearEnv: true,
		},
		{
			name:     "uses custom namespace from env var",
			envValue: "custom-namespace",
			expected: "custom-namespace",
			setupEnv: true,
		},
		{
			name:     "uses custom namespace with special characters",
			envValue: "test-namespace-123",
			expected: "test-namespace-123",
			setupEnv: true,
		},
		{
			name:     "handles empty env var",
			envValue: "",
			expected: DefaultOperatorNamespace,
			setupEnv: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env value
			originalEnv := os.Getenv(OperatorNamespaceEnvVar)
			defer func() {
				if originalEnv != "" {
					os.Setenv(OperatorNamespaceEnvVar, originalEnv)
				} else {
					os.Unsetenv(OperatorNamespaceEnvVar)
				}
			}()

			// Setup test environment
			if tt.clearEnv {
				os.Unsetenv(OperatorNamespaceEnvVar)
			} else if tt.setupEnv {
				os.Setenv(OperatorNamespaceEnvVar, tt.envValue)
			}

			// Test the function
			result := GetOperatorNamespace()
			if result != tt.expected {
				t.Errorf("GetOperatorNamespace() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	if DefaultOperatorNamespace != "must-gather-operator" {
		t.Errorf("DefaultOperatorNamespace = %v, want %v", DefaultOperatorNamespace, "must-gather-operator")
	}

	if OperatorNamespaceEnvVar != "OPERATOR_NAMESPACE" {
		t.Errorf("OperatorNamespaceEnvVar = %v, want %v", OperatorNamespaceEnvVar, "OPERATOR_NAMESPACE")
	}
}
