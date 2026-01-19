package mustgather

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	// Timeout for SFTP connection validation
	sftpValidationTimeout = 10 * time.Second

	// Timeout for individual SSH dial operations
	sshDialTimeout = 5 * time.Second

	// Default SFTP port
	sftpDefaultPort = "22"
)

// sftpDialFunc is the function used to test SFTP connections.
// It can be overridden in tests to avoid real network calls.
var sftpDialFunc = testSFTPConnection

// TransientError wraps an error that should trigger a requeue rather than permanent failure
type TransientError struct {
	Err error
}

// Error returns the error message for TransientError, implementing the error interface.
func (e *TransientError) Error() string {
	return fmt.Sprintf("transient error: %v", e.Err)
}

// Unwrap returns the underlying error, implementing the errors.Unwrap interface.
func (e *TransientError) Unwrap() error {
	return e.Err
}

// IsTransientError checks if an error is transient and should trigger a requeue
func IsTransientError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var transientErr *TransientError
	return errors.As(err, &transientErr)
}

// validateSFTPCredentials tests SFTP connection with the provided credentials.
//
// The username and password parameters must be non-empty (validated by caller).
// The host parameter is expected to be non-empty (enforced by CRD default value).
//
// The function performs validation with a 10-second timeout.
//
// It returns an error if:
// - Connection to SFTP host fails
// - Authentication with provided credentials fails
// - Validation times out after 10 seconds
//
// Security: This function ALWAYS uses InsecureIgnoreHostKey (no host key verification)
// to match the upload script behavior (StrictHostKeyChecking=no). This is vulnerable to
// MITM attacks and NOT RECOMMENDED for production use.
func validateSFTPCredentials(
	ctx context.Context,
	username string,
	password string,
	host string,
) error {
	// Test SFTP connection with timeout
	validationCtx, cancel := context.WithTimeout(ctx, sftpValidationTimeout)
	defer cancel()

	// Run validation in a goroutine to respect context timeout
	// Pass validationCtx to allow cancellation of SSH operations
	// Buffer size 1 prevents goroutine from blocking when writing to channel if context times out
	errChan := make(chan error, 1)
	go func() {
		// Recover from panics to prevent goroutine leaks if SSH library panics unexpectedly
		defer func() {
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("SFTP validation panicked: %v", r)
			}
		}()
		errChan <- sftpDialFunc(validationCtx, username, password, host)
	}()

	select {
	case err := <-errChan:
		return err
	case <-validationCtx.Done():
		return &TransientError{Err: fmt.Errorf("SFTP credential validation timed out after %v", sftpValidationTimeout)}
	}
}

// testSFTPConnection attempts to establish an SFTP connection and authenticate.
// This is a lightweight test that only checks credentials without transferring files.
//
// The context is used to cancel the SSH connection if the validation times out.
//
// The host parameter accepts:
// - IPv4: "hostname" or "hostname:port" or "192.0.2.1" or "192.0.2.1:2222"
// - IPv6: "[2001:db8::1]" or "[2001:db8::1]:2222" or "2001:db8::1" (auto-bracketed)
// If no port is specified, the default SFTP port (22) is used.
func testSFTPConnection(ctx context.Context, username, password, host string) error {
	address := normalizeHostAddress(host)

	// Skip host key verification to match upload script (StrictHostKeyChecking=no)
	// #nosec G106 -- Intentional: matches upload script behavior
	config := buildSSHConfig(username, password, ssh.InsecureIgnoreHostKey())
	conn, err := dialSSHWithContext(ctx, address, config)
	if err != nil {
		return err
	}
	defer conn.Close()

	return verifySFTPSubsystem(conn)
}

// normalizeHostAddress adds the default SFTP port if not specified.
// For IPv6 addresses without brackets, it wraps them in brackets before adding the port
// to avoid malformed addresses like 2001:db8::1:22.
func normalizeHostAddress(host string) string {
	if host == "" || containsPort(host) {
		return host
	}

	// Check if this looks like an IPv6 address (contains colons but not bracketed)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		// Unbracketed IPv6 address - wrap in brackets before adding port
		return fmt.Sprintf("[%s]:%s", host, sftpDefaultPort)
	}

	// IPv4 or already-bracketed IPv6 - add port normally
	return fmt.Sprintf("%s:%s", host, sftpDefaultPort)
}

// buildSSHConfig creates an SSH client configuration with the provided credentials and host key callback.
func buildSSHConfig(username, password string, hostKeyCallback ssh.HostKeyCallback) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         sshDialTimeout,
	}
}

// dialResult holds the result of an SSH dial operation.
type dialResult struct {
	conn *ssh.Client
	err  error
}

// dialSSHWithContext attempts to establish an SSH connection with context cancellation support.
// It uses a goroutine and channel to allow the context to cancel the operation.
func dialSSHWithContext(ctx context.Context, address string, config *ssh.ClientConfig) (*ssh.Client, error) {
	// Buffer size 1 prevents goroutine from blocking when writing to channel if context is cancelled
	dialChan := make(chan dialResult, 1)
	go func() {
		// Recover from panics to prevent goroutine leaks if SSH library panics unexpectedly
		defer func() {
			if r := recover(); r != nil {
				dialChan <- dialResult{conn: nil, err: fmt.Errorf("ssh dial panicked: %v", r)}
			}
		}()
		conn, err := ssh.Dial("tcp", address, config)
		// Use non-blocking select to avoid leaking connections if ctx is cancelled
		// after successful dial but before the outer select receives the result
		select {
		case dialChan <- dialResult{conn: conn, err: err}:
			// Successfully sent result to channel
		case <-ctx.Done():
			// Context was cancelled before we could send result - close connection to avoid leak
			if conn != nil {
				conn.Close()
			}
		}
	}()

	select {
	case result := <-dialChan:
		if result.err != nil {
			return nil, fmt.Errorf("SFTP connection failed: %w", result.err)
		}
		return result.conn, nil
	case <-ctx.Done():
		// Context cancelled - connection will be abandoned
		return nil, fmt.Errorf("SFTP connection cancelled: %w", ctx.Err())
	}
}

// verifySFTPSubsystem verifies that the SFTP subsystem is available on the SSH connection.
// This creates and immediately closes an SFTP client to confirm functionality.
func verifySFTPSubsystem(conn *ssh.Client) error {
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	// Success - connection and authentication worked
	return nil
}

// containsPort checks if the host string already contains a port.
// Returns true for valid host:port combinations like "hostname:22" or "[::1]:22".
// Returns false for:
// - IPv4 without port: "hostname" or "192.0.2.1"
// - IPv6 without port: "[2001:db8::1]" or unbracketed "2001:db8::1"
func containsPort(host string) bool {
	_, _, err := net.SplitHostPort(host)
	// If SplitHostPort succeeds, a valid host:port was present
	return err == nil
}
