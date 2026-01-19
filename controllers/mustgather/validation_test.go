package mustgather

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// mockNetError is a mock implementation of net.Error for testing
type mockNetError struct {
	message   string
	timeout   bool
	temporary bool
}

func (e *mockNetError) Error() string   { return e.message }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }

// Verify mockNetError implements net.Error
var _ net.Error = (*mockNetError)(nil)

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

func Test_normalizeHostAddress(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{
			name:     "empty host",
			host:     "",
			expected: "",
		},
		{
			name:     "IPv4 without port",
			host:     "192.168.1.1",
			expected: "192.168.1.1:22",
		},
		{
			name:     "IPv4 with port",
			host:     "192.168.1.1:2222",
			expected: "192.168.1.1:2222",
		},
		{
			name:     "hostname without port",
			host:     "example.com",
			expected: "example.com:22",
		},
		{
			name:     "hostname with port",
			host:     "example.com:2222",
			expected: "example.com:2222",
		},
		{
			name:     "IPv6 unbracketed",
			host:     "2001:db8::1",
			expected: "[2001:db8::1]:22",
		},
		{
			name:     "IPv6 bracketed without port",
			host:     "[2001:db8::1]",
			expected: "[2001:db8::1]:22",
		},
		{
			name:     "IPv6 bracketed with port",
			host:     "[2001:db8::1]:2222",
			expected: "[2001:db8::1]:2222",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHostAddress(tt.host)
			if got != tt.expected {
				t.Errorf("normalizeHostAddress(%q) = %q, want %q", tt.host, got, tt.expected)
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
		{
			name:     "net.Error with timeout true",
			err:      &mockNetError{message: "connection timed out", timeout: true},
			expected: true,
		},
		{
			name:     "net.Error with timeout false",
			err:      &mockNetError{message: "connection refused", timeout: false},
			expected: false,
		},
		{
			name:     "wrapped net.Error with timeout true",
			err:      fmt.Errorf("dial failed: %w", &mockNetError{message: "i/o timeout", timeout: true}),
			expected: true,
		},
		{
			name:     "wrapped net.Error with timeout false",
			err:      fmt.Errorf("dial failed: %w", &mockNetError{message: "connection refused", timeout: false}),
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
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty host",
			username:    "user",
			password:    "pass",
			host:        "",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv4 without port",
			username:    "user",
			password:    "pass",
			host:        "192.168.1.1",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv4 with port",
			username:    "user",
			password:    "pass",
			host:        "192.168.1.1:22",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "hostname without port",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "hostname with port",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com:2222",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv6 unbracketed without port",
			username:    "user",
			password:    "pass",
			host:        "2001:db8::1",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv6 bracketed without port",
			username:    "user",
			password:    "pass",
			host:        "[2001:db8::1]",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "IPv6 bracketed with port",
			username:    "user",
			password:    "pass",
			host:        "[2001:db8::1]:2222",
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := testSFTPConnection(ctx, tt.username, tt.password, tt.host)

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

			err := testSFTPConnection(ctx, tt.username, tt.password, tt.host)

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
		mockDialErr error
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid credentials - connection fails",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			mockDialErr: fmt.Errorf("SFTP connection failed: ssh: handshake failed"),
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "valid credentials - auth fails",
			username:    "user",
			password:    "wrongpass",
			host:        "sftp.example.com",
			mockDialErr: fmt.Errorf("SFTP connection failed: ssh: unable to authenticate"),
			wantErr:     true,
			errContains: "SFTP connection failed",
		},
		{
			name:        "valid credentials - success",
			username:    "user",
			password:    "pass",
			host:        "sftp.example.com",
			mockDialErr: nil,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock the dial function
			originalDialFunc := sftpDialFunc
			defer func() { sftpDialFunc = originalDialFunc }()

			sftpDialFunc = func(ctx context.Context, username, password, host string) error {
				return tt.mockDialErr
			}

			// Run the validation
			err := validateSFTPCredentials(context.Background(), tt.username, tt.password, tt.host)

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

	sftpDialFunc = func(ctx context.Context, username, password, host string) error {
		// Block until context is cancelled
		<-ctx.Done()
		return ctx.Err()
	}

	// Run the validation (should timeout after 10 seconds, but we'll use a shorter context)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := validateSFTPCredentials(ctx, "user", "pass", "sftp.example.com")

	if err == nil {
		t.Errorf("validateSFTPCredentials() expected timeout error, got nil")
	} else if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("validateSFTPCredentials() error = %v, want error containing 'timed out'", err)
	}
}
