package mustgather

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_containsPort(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected bool
	}{
		{
			name:     "IPv4 with port",
			host:     "192.168.1.1:22",
			expected: true,
		},
		{
			name:     "IPv4 without port",
			host:     "192.168.1.1",
			expected: false,
		},
		{
			name:     "hostname with port",
			host:     "example.com:22",
			expected: true,
		},
		{
			name:     "hostname without port",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "IPv6 bracketed with port",
			host:     "[2001:db8::1]:22",
			expected: true,
		},
		{
			name:     "IPv6 bracketed without port",
			host:     "[2001:db8::1]",
			expected: false,
		},
		{
			name:     "IPv6 unbracketed without port",
			host:     "2001:db8::1",
			expected: false,
		},
		{
			name:     "localhost with port",
			host:     "localhost:2222",
			expected: true,
		},
		{
			name:     "localhost without port",
			host:     "localhost",
			expected: false,
		},
		{
			name:     "empty string",
			host:     "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsPort(tt.host)
			if got != tt.expected {
				t.Errorf("containsPort(%q) = %v, want %v", tt.host, got, tt.expected)
			}
		})
	}
}

func Test_isTransientAPIError(t *testing.T) {
	// Create a test resource reference for errors
	resource := schema.GroupResource{Group: "v1", Resource: "secrets"}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "timeout error",
			err:      apierrors.NewTimeoutError("timeout", 5),
			expected: true,
		},
		{
			name:     "server timeout error",
			err:      apierrors.NewServerTimeout(resource, "get", 5),
			expected: true,
		},
		{
			name:     "too many requests error",
			err:      apierrors.NewTooManyRequests("too many requests", 5),
			expected: true,
		},
		{
			name:     "service unavailable error",
			err:      apierrors.NewServiceUnavailable("service unavailable"),
			expected: true,
		},
		{
			name:     "internal server error",
			err:      apierrors.NewInternalError(errors.New("internal error")),
			expected: true,
		},
		{
			name:     "not found error",
			err:      apierrors.NewNotFound(resource, "test-secret"),
			expected: false,
		},
		{
			name:     "already exists error",
			err:      apierrors.NewAlreadyExists(resource, "test-secret"),
			expected: false,
		},
		{
			name:     "unauthorized error",
			err:      apierrors.NewUnauthorized("unauthorized"),
			expected: false,
		},
		{
			name:     "forbidden error",
			err:      apierrors.NewForbidden(resource, "test-secret", errors.New("forbidden")),
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("generic error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientAPIError(tt.err)
			if got != tt.expected {
				t.Errorf("isTransientAPIError() = %v, want %v for error: %v", got, tt.expected, tt.err)
			}
		})
	}
}

func Test_IsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "transient error",
			err:      &TransientError{Err: errors.New("network timeout")},
			expected: true,
		},
		{
			name:     "wrapped transient error",
			err:      fmt.Errorf("wrapped: %w", &TransientError{Err: errors.New("timeout")}),
			expected: true,
		},
		{
			name:     "regular error",
			err:      errors.New("regular error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "wrapped regular error",
			err:      fmt.Errorf("wrapped: %w", errors.New("regular")),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTransientError(tt.err)
			if got != tt.expected {
				t.Errorf("IsTransientError() = %v, want %v for error: %v", got, tt.expected, tt.err)
			}
		})
	}
}

func Test_TransientError_Error(t *testing.T) {
	tests := []struct {
		name        string
		err         *TransientError
		expectedMsg string
	}{
		{
			name:        "simple error message",
			err:         &TransientError{Err: errors.New("connection timeout")},
			expectedMsg: "transient error: connection timeout",
		},
		{
			name:        "formatted error message",
			err:         &TransientError{Err: fmt.Errorf("failed to connect: %w", errors.New("timeout"))},
			expectedMsg: "transient error: failed to connect: timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expectedMsg {
				t.Errorf("Error() = %q, want %q", got, tt.expectedMsg)
			}
		})
	}
}

func Test_TransientError_Unwrap(t *testing.T) {
	originalErr := errors.New("original error")
	transientErr := &TransientError{Err: originalErr}

	unwrapped := transientErr.Unwrap()
	if unwrapped != originalErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, originalErr)
	}
}

func Test_testSFTPConnection(t *testing.T) {
	tests := []struct {
		name        string
		username    string
		password    string
		host        string
		hostKeyData string
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty host",
			username:    "user",
			password:    "pass",
			host:        "",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv4 without port",
			username:    "user",
			password:    "pass",
			host:        "192.168.1.1",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv4 with port",
			username:    "user",
			password:    "pass",
			host:        "192.168.1.1:22",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "hostname without port",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "hostname with port",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com:2222",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv6 unbracketed without port",
			username:    "user",
			password:    "pass",
			host:        "2001:db8::1",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv6 bracketed without port",
			username:    "user",
			password:    "pass",
			host:        "[2001:db8::1]",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv6 bracketed with port",
			username:    "user",
			password:    "pass",
			host:        "[2001:db8::1]:2222",
			hostKeyData: "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "invalid host key data",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			hostKeyData: "invalid host key",
			wantErr:     true,
			errContains: "failed to parse known_hosts data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := testSFTPConnection(ctx, tt.username, tt.password, tt.host, tt.hostKeyData)

			if tt.wantErr {
				if err == nil {
					t.Errorf("testSFTPConnection() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("testSFTPConnection() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("testSFTPConnection() unexpected error = %v", err)
				}
			}
		})
	}
}

func Test_testSFTPConnection_ContextCancellation(t *testing.T) {
	tests := []struct {
		name        string
		username    string
		password    string
		host        string
		timeout     time.Duration
		wantErr     bool
		errContains string
	}{
		{
			name:        "context cancelled before connection",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			timeout:     1 * time.Nanosecond,
			wantErr:     true,
			errContains: "SFTP connection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			// Wait for context to be cancelled
			time.Sleep(10 * time.Millisecond)

			err := testSFTPConnection(ctx, tt.username, tt.password, tt.host, "")

			if tt.wantErr {
				if err == nil {
					t.Errorf("testSFTPConnection() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("testSFTPConnection() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

func Test_validateSFTPCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name          string
		secretRef     corev1.LocalObjectReference
		host          string
		namespace     string
		secret        *corev1.Secret
		mockDialErr   error
		wantErr       bool
		errContains   string
		wantTransient bool
	}{
		{
			name:          "empty host",
			secretRef:     corev1.LocalObjectReference{Name: "test-secret"},
			host:          "",
			namespace:     "default",
			wantErr:       true,
			errContains:   "SFTP host cannot be empty",
			wantTransient: false,
		},
		{
			name:          "whitespace only host",
			secretRef:     corev1.LocalObjectReference{Name: "test-secret"},
			host:          "   ",
			namespace:     "default",
			wantErr:       true,
			errContains:   "SFTP host cannot be empty",
			wantTransient: false,
		},
		{
			name:          "secret not found",
			secretRef:     corev1.LocalObjectReference{Name: "missing-secret"},
			host:          "sftp.example.com",
			namespace:     "default",
			wantErr:       true,
			errContains:   "failed to retrieve SFTP credentials secret",
			wantTransient: false,
		},
		{
			name:      "secret missing username",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"password": []byte("pass"),
				},
			},
			wantErr:       true,
			errContains:   "missing required field 'username'",
			wantTransient: false,
		},
		{
			name:      "secret username empty",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte(""),
					"password": []byte("pass"),
				},
			},
			wantErr:       true,
			errContains:   "missing required field 'username'",
			wantTransient: false,
		},
		{
			name:      "secret missing password",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
				},
			},
			wantErr:       true,
			errContains:   "missing required field 'password'",
			wantTransient: false,
		},
		{
			name:      "secret password empty",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte(""),
				},
			},
			wantErr:       true,
			errContains:   "missing required field 'password'",
			wantTransient: false,
		},
		{
			name:      "valid credentials - connection fails",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
				},
			},
			mockDialErr:   fmt.Errorf("SFTP connection failed: ssh: handshake failed"),
			wantErr:       true,
			errContains:   "SFTP connection failed",
			wantTransient: false,
		},
		{
			name:      "valid credentials - auth fails",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("wrongpass"),
				},
			},
			mockDialErr:   fmt.Errorf("SFTP connection failed: ssh: unable to authenticate"),
			wantErr:       true,
			errContains:   "SFTP connection failed",
			wantTransient: false,
		},
		{
			name:      "valid credentials - success",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
				},
			},
			mockDialErr: nil,
			wantErr:     false,
		},
		{
			name:      "valid credentials with host key",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
					"host_key": []byte("sftp.example.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."),
				},
			},
			mockDialErr: nil,
			wantErr:     false,
		},
		{
			name:      "valid credentials with empty host key",
			secretRef: corev1.LocalObjectReference{Name: "test-secret"},
			host:      "sftp.example.com",
			namespace: "default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
					"host_key": []byte(""),
				},
			},
			mockDialErr: nil,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client
			var k8sClient client.Client
			if tt.secret != nil {
				k8sClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.secret).Build()
			} else {
				k8sClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			}

			// Mock the dial function
			originalDialFunc := sftpDialFunc
			defer func() { sftpDialFunc = originalDialFunc }()

			sftpDialFunc = func(ctx context.Context, username, password, host, hostKeyData string) error {
				return tt.mockDialErr
			}

			// Run the validation
			err := validateSFTPCredentials(context.Background(), k8sClient, tt.secretRef, tt.host, tt.namespace)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateSFTPCredentials() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateSFTPCredentials() error = %v, want error containing %q", err, tt.errContains)
				}

				// Check if error is transient when expected
				if tt.wantTransient != IsTransientError(err) {
					t.Errorf("validateSFTPCredentials() transient error = %v, want %v", IsTransientError(err), tt.wantTransient)
				}
			} else {
				if err != nil {
					t.Errorf("validateSFTPCredentials() unexpected error = %v", err)
				}
			}
		})
	}
}

func Test_validateSFTPCredentials_TransientAPIErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	resource := schema.GroupResource{Group: "v1", Resource: "secrets"}

	tests := []struct {
		name          string
		apiError      error
		wantTransient bool
	}{
		{
			name:          "timeout error",
			apiError:      apierrors.NewTimeoutError("timeout", 5),
			wantTransient: true,
		},
		{
			name:          "server timeout error",
			apiError:      apierrors.NewServerTimeout(resource, "get", 5),
			wantTransient: true,
		},
		{
			name:          "too many requests error",
			apiError:      apierrors.NewTooManyRequests("too many requests", 5),
			wantTransient: true,
		},
		{
			name:          "service unavailable error",
			apiError:      apierrors.NewServiceUnavailable("service unavailable"),
			wantTransient: true,
		},
		{
			name:          "internal error",
			apiError:      apierrors.NewInternalError(errors.New("internal error")),
			wantTransient: true,
		},
		{
			name:          "not found error",
			apiError:      apierrors.NewNotFound(resource, "test-secret"),
			wantTransient: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client that returns the specified error
			k8sClient := &fakeClientWithError{
				err: tt.apiError,
			}

			// Run the validation
			err := validateSFTPCredentials(
				context.Background(),
				k8sClient,
				corev1.LocalObjectReference{Name: "test-secret"},
				"sftp.example.com",
				"default",
			)

			if err == nil {
				t.Errorf("validateSFTPCredentials() expected error, got nil")
				return
			}

			// Check if error is transient
			isTransient := IsTransientError(err)
			if isTransient != tt.wantTransient {
				t.Errorf("validateSFTPCredentials() transient error = %v, want %v for error: %v", isTransient, tt.wantTransient, tt.apiError)
			}
		})
	}
}

func Test_validateSFTPCredentials_Timeout(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"username": []byte("user"),
			"password": []byte("pass"),
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	// Mock the dial function to block indefinitely
	originalDialFunc := sftpDialFunc
	defer func() { sftpDialFunc = originalDialFunc }()

	sftpDialFunc = func(ctx context.Context, username, password, host, hostKeyData string) error {
		// Block until context is cancelled
		<-ctx.Done()
		return ctx.Err()
	}

	// Run the validation (should timeout after 10 seconds, but we'll use a shorter context)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := validateSFTPCredentials(ctx, k8sClient, corev1.LocalObjectReference{Name: "test-secret"}, "sftp.example.com", "default")

	if err == nil {
		t.Errorf("validateSFTPCredentials() expected timeout error, got nil")
	} else if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("validateSFTPCredentials() error = %v, want error containing 'timed out'", err)
	}
}

// Helper type to simulate client errors
type fakeClientWithError struct {
	client.Client
	err error
}

func (f *fakeClientWithError) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return f.err
}
