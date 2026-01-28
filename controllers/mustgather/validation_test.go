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
			name:     "context canceled",
			err:      context.Canceled,
			expected: true,
		},
		{
			name:     "wrapped context canceled",
			err:      fmt.Errorf("operation aborted: %w", context.Canceled),
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

func Test_checkSFTPConnection(t *testing.T) {
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
			// Stub the net dialer to avoid real network I/O
			originalNetDialFunc := netDialFunc
			defer func() { netDialFunc = originalNetDialFunc }()

			netDialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return nil, errors.New("connection refused")
			}

			err := checkSFTPConnection(context.Background(), tt.username, tt.password, tt.host)

			if tt.wantErr {
				if err == nil {
					t.Errorf("checkSFTPConnection() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("checkSFTPConnection() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("checkSFTPConnection() unexpected error = %v", err)
				}
			}
		})
	}
}

func Test_checkSFTPConnection_ContextCancellation(t *testing.T) {
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
			// Stub the net dialer to simulate slow connection
			originalNetDialFunc := netDialFunc
			defer func() { netDialFunc = originalNetDialFunc }()

			netDialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Check if context is already cancelled
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				// Simulate a slow connection that respects context cancellation
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(100 * time.Millisecond):
					return nil, errors.New("connection timed out")
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			err := checkSFTPConnection(ctx, tt.username, tt.password, tt.host)

			if tt.wantErr {
				if err == nil {
					t.Errorf("checkSFTPConnection() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("checkSFTPConnection() error = %v, want error containing %q", err, tt.errContains)
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
	// Mock the dial function to block until context is cancelled
	originalDialFunc := sftpDialFunc
	defer func() { sftpDialFunc = originalDialFunc }()

	// Create a context with timeout
	testCtx, testCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer testCancel()

	sftpDialFunc = func(ctx context.Context, username, password, host string) error {
		// Block until the passed context is cancelled (respecting context cancellation)
		<-ctx.Done()
		return ctx.Err()
	}

	// Run the validation (should timeout based on context)
	err := validateSFTPCredentials(testCtx, "user", "pass", "sftp.example.com")

	if err == nil {
		t.Errorf("validateSFTPCredentials() expected timeout error, got nil")
	} else if !IsTransientError(err) {
		t.Errorf("validateSFTPCredentials() error = %v, want transient error", err)
	}
}
