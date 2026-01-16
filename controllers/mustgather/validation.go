package mustgather

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// Timeout for SFTP connection validation
	sftpValidationTimeout = 10 * time.Second

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
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var transientErr *TransientError
	return errors.As(err, &transientErr)
}

// validateSFTPCredentials tests SFTP connection with the provided credentials.
//
// The username and password parameters must be non-empty (validated by caller).
// The host parameter is expected to be non-empty (enforced by CRD default value).
// The hostKeyData parameter is optional and should contain SSH known_hosts format data.
//
// The function performs validation with a 10-second timeout.
//
// It returns an error if:
// - Connection to SFTP host fails
// - Authentication with provided credentials fails
// - Validation times out after 10 seconds
//
// Security: When hostKeyData is not provided, connections use InsecureIgnoreHostKey which
// is vulnerable to MITM attacks and NOT RECOMMENDED for production use.
func validateSFTPCredentials(
	ctx context.Context,
	username string,
	password string,
	host string,
	hostKeyData string,
) error {
	// Test SFTP connection with timeout
	validationCtx, cancel := context.WithTimeout(ctx, sftpValidationTimeout)
	defer cancel()

	// Run validation in a goroutine to respect context timeout
	// Pass validationCtx to allow cancellation of SSH operations
	errChan := make(chan error, 1)
	go func() {
		errChan <- sftpDialFunc(validationCtx, username, password, host, hostKeyData)
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
//
// The hostKeyData parameter should contain SSH known_hosts format data for host verification.
// If hostKeyData is empty, the connection will use ssh.InsecureIgnoreHostKey() which skips host
// key verification (NOT RECOMMENDED for production - vulnerable to MITM attacks).
// When hostKeyData is provided, it uses knownhosts.New() for secure OpenSSH-compatible verification.
func testSFTPConnection(ctx context.Context, username, password, host, hostKeyData string) error {
	address := normalizeHostAddress(host)
	hostKeyCallback, err := buildHostKeyCallback(hostKeyData)
	if err != nil {
		return err
	}

	config := buildSSHConfig(username, password, hostKeyCallback)
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

// buildHostKeyCallback creates an SSH host key callback based on the provided host key data.
// If hostKeyData is empty, it returns InsecureIgnoreHostKey (NOT RECOMMENDED for production).
// If hostKeyData is provided, it creates a proper known_hosts-based callback.
func buildHostKeyCallback(hostKeyData string) (ssh.HostKeyCallback, error) {
	if strings.TrimSpace(hostKeyData) == "" {
		// WARNING: No host_key provided - using InsecureIgnoreHostKey
		// This skips host key verification and is VULNERABLE to Man-in-the-Middle (MITM) attacks
		// For production use, always provide host_key in the secret to enable secure verification
		// #nosec G106 -- Intentional use when host_key not provided, documented security trade-off
		return ssh.InsecureIgnoreHostKey(), nil
	}

	return createKnownHostsCallback(hostKeyData)
}

// createKnownHostsCallback creates a host key callback from known_hosts format data.
// It creates a temporary file to hold the data since knownhosts.New() requires a file path.
// The temporary file is automatically cleaned up.
func createKnownHostsCallback(hostKeyData string) (ssh.HostKeyCallback, error) {
	// Use golang.org/x/crypto/ssh/knownhosts for production-grade known_hosts parsing
	// This properly handles:
	// - Hashed hostnames (|1|...|...)
	// - Wildcard patterns (*.example.com, !*.internal)
	// - Standard OpenSSH known_hosts formats

	// knownhosts.New() requires a file path, so create a temporary file
	// to hold the in-memory hostKeyData from the Kubernetes secret
	tmpFile, err := os.CreateTemp("", "known_hosts_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary known_hosts file: %w", err)
	}
	tmpFilePath := tmpFile.Name()
	defer os.Remove(tmpFilePath) // Clean up temp file

	// Write the known_hosts data to the temporary file
	if _, err := tmpFile.WriteString(hostKeyData); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write known_hosts data to temporary file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temporary known_hosts file: %w", err)
	}

	// Use knownhosts.New() to get a proper callback with full OpenSSH support
	callback, err := knownhosts.New(tmpFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse known_hosts data: %w", err)
	}

	return callback, nil
}

// buildSSHConfig creates an SSH client configuration with the provided credentials and host key callback.
func buildSSHConfig(username, password string, hostKeyCallback ssh.HostKeyCallback) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
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
	dialChan := make(chan dialResult, 1)
	go func() {
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
