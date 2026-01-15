package mustgather

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// isTransientAPIError checks if an error from the API server is transient.
// Transient errors should trigger a requeue rather than permanent failure.
//
// Returns true for: Timeout, ServerTimeout, TooManyRequests, ServiceUnavailable, InternalError
func isTransientAPIError(err error) bool {
	// Check for transient errors that should trigger a requeue
	return apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err)
}

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
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: true,
		},
		{
			name:     "wrapped context deadline exceeded",
			err:      fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
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
	tests := []struct {
		name        string
		username    string
		password    string
		host        string
		hostKeyData string
		mockDialErr error
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid credentials - connection fails",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			hostKeyData: "",
			mockDialErr: fmt.Errorf("SFTP connection failed: ssh: handshake failed"),
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "valid credentials - auth fails",
			username:    "user",
			password:    "wrongpass",
			host:        "sftp.example.com",
			hostKeyData: "",
			mockDialErr: fmt.Errorf("SFTP connection failed: ssh: unable to authenticate"),
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "valid credentials - success",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			hostKeyData: "",
			mockDialErr: nil,
			wantErr:     false,
		},
		{
			name:        "valid credentials with host key",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			hostKeyData: "sftp.example.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...",
			mockDialErr: nil,
			wantErr:     false,
		},
		{
			name:        "valid credentials with empty host key",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			hostKeyData: "",
			mockDialErr: nil,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock the dial function
			originalDialFunc := sftpDialFunc
			defer func() { sftpDialFunc = originalDialFunc }()

			sftpDialFunc = func(ctx context.Context, username, password, host, hostKeyData string) error {
				return tt.mockDialErr
			}

			// Run the validation
			err := validateSFTPCredentials(context.Background(), tt.username, tt.password, tt.host, tt.hostKeyData)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateSFTPCredentials() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateSFTPCredentials() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("validateSFTPCredentials() unexpected error = %v", err)
				}
			}
		})
	}
}

func Test_validateSFTPCredentials_Timeout(t *testing.T) {
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

	err := validateSFTPCredentials(ctx, "user", "pass", "sftp.example.com", "")

	if err == nil {
		t.Errorf("validateSFTPCredentials() expected timeout error, got nil")
	} else if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("validateSFTPCredentials() error = %v, want error containing 'timed out'", err)
	}
}
