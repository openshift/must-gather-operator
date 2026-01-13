package mustgather

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (e *TransientError) Error() string {
	return fmt.Sprintf("transient error: %v", e.Err)
}

func (e *TransientError) Unwrap() error {
	return e.Err
}

// IsTransientError checks if an error is transient and should trigger a requeue
func IsTransientError(err error) bool {
	var transientErr *TransientError
	return errors.As(err, &transientErr)
}

// validateSFTPCredentials tests SFTP connection with the provided credentials.
// It returns an error if:
// - The secret doesn't exist or is missing required fields (may be transient if API server error)
// - Connection to SFTP host fails
// - Authentication with provided credentials fails
//
// Transient errors (API timeouts, network issues) are wrapped in TransientError
// for the caller to requeue. Permanent validation failures return regular errors.
func validateSFTPCredentials(
	ctx context.Context,
	k8sClient client.Client,
	secretRef corev1.LocalObjectReference,
	host string,
	namespace string,
) error {
	// Validate host is not empty before attempting connection
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("SFTP host cannot be empty")
	}

	// Retrieve the secret
	secret := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      secretRef.Name,
	}, secret)
	if err != nil {
		// Check for transient API server errors that should trigger requeue
		if isTransientAPIError(err) {
			return &TransientError{
				Err: fmt.Errorf("failed to retrieve SFTP credentials secret (transient): %w", err),
			}
		}
		// Non-transient error (e.g., NotFound) - permanent validation failure
		return fmt.Errorf("failed to retrieve SFTP credentials secret: %w", err)
	}

	// Validate required fields
	username, usernameExists := secret.Data["username"]
	password, passwordExists := secret.Data["password"]

	if !usernameExists {
		return fmt.Errorf("SFTP credentials secret '%s' is missing required field 'username'", secretRef.Name)
	}
	if !passwordExists {
		return fmt.Errorf("SFTP credentials secret '%s' is missing required field 'password'", secretRef.Name)
	}

	// Retrieve optional host key for verification
	// If not provided, testSFTPConnection will fail with a security error
	hostKey, _ := secret.Data["host_key"]
	hostKeyData := string(hostKey)

	// Test SFTP connection with timeout
	validationCtx, cancel := context.WithTimeout(ctx, sftpValidationTimeout)
	defer cancel()

	// Run validation in a goroutine to respect context timeout
	// Pass validationCtx to allow cancellation of SSH operations
	errChan := make(chan error, 1)
	go func() {
		errChan <- sftpDialFunc(validationCtx, string(username), string(password), host, hostKeyData)
	}()

	select {
	case err := <-errChan:
		return err
	case <-validationCtx.Done():
		return fmt.Errorf("SFTP credential validation timed out after %v", sftpValidationTimeout)
	}
}

// isTransientAPIError checks if an error from the API server is transient
func isTransientAPIError(err error) bool {
	// Check for transient errors that should trigger a requeue
	return apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err)
}

// testSFTPConnection attempts to establish an SFTP connection and authenticate.
// This is a lightweight test that only checks credentials without transferring files.
//
// The context is used to cancel the SSH connection if the validation times out.
// The hostKeyData parameter should contain SSH known_hosts format data for host verification.
// If hostKeyData is empty, the connection will fail with an error (host key verification is required).
func testSFTPConnection(ctx context.Context, username, password, host, hostKeyData string) error {
	// Add default port if not specified
	address := host
	if address != "" && address[len(address)-1] != ':' && !containsPort(address) {
		address = fmt.Sprintf("%s:%s", address, sftpDefaultPort)
	}

	// Create host key callback - REQUIRED for security
	var hostKeyCallback ssh.HostKeyCallback
	if strings.TrimSpace(hostKeyData) == "" {
		// Host key verification is REQUIRED - fail if not provided
		return fmt.Errorf("SFTP host key verification data is required but not provided; refusing to connect without host key verification to prevent MITM attacks")
	}

	// Parse known_hosts format data
	// Create a temporary in-memory knownhosts callback
	hostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// Parse each line of the known_hosts data
		for _, line := range strings.Split(hostKeyData, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Parse known_hosts line format: "hostname keytype key"
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}

			// fields[0] = hostname pattern
			// fields[1] = key type
			// fields[2] = base64-encoded key

			// Simple hostname match (could be enhanced with pattern matching)
			expectedHost := fields[0]
			if expectedHost != hostname && expectedHost != host {
				continue
			}

			// Decode the public key
			keyBytes, err := base64.StdEncoding.DecodeString(fields[2])
			if err != nil {
				continue
			}

			// Parse the public key
			expectedKey, err := ssh.ParsePublicKey(keyBytes)
			if err != nil {
				continue
			}

			// Compare keys
			if bytes.Equal(key.Marshal(), expectedKey.Marshal()) {
				return nil // Host key matches!
			}
		}

		// No matching key found
		return fmt.Errorf("ssh: host key verification failed for %s: provided host key does not match known_hosts data", hostname)
	}

	// Configure SSH client with proper host key verification
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
	}

	// Attempt SSH connection with context cancellation support
	// Use a channel to allow context cancellation during connection
	type dialResult struct {
		conn *ssh.Client
		err  error
	}

	dialChan := make(chan dialResult, 1)
	go func() {
		conn, err := ssh.Dial("tcp", address, config)
		dialChan <- dialResult{conn: conn, err: err}
	}()

	var conn *ssh.Client
	select {
	case result := <-dialChan:
		if result.err != nil {
			return fmt.Errorf("SFTP authentication failed: %w", result.err)
		}
		conn = result.conn
	case <-ctx.Done():
		// Context cancelled - connection will be abandoned
		return fmt.Errorf("SFTP connection cancelled: %w", ctx.Err())
	}
	defer conn.Close()

	// Attempt to create SFTP client
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	// Success - connection and authentication worked
	return nil
}

// containsPort checks if the host string already contains a port
func containsPort(host string) bool {
	// Simple check for port in host string
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return true
		}
		if host[i] == ']' {
			// IPv6 address without port
			return false
		}
	}
	return false
}
