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

	// Retrieve optional host_key for verification
	// If not provided, testSFTPConnection will use InsecureIgnoreHostKey (NOT RECOMMENDED for production)
	// For production use, always provide host_key to prevent MITM attacks
	hostKeyData := ""
	if hostKey, hostKeyExists := secret.Data["host_key"]; hostKeyExists {
		hostKeyData = string(hostKey)
	}

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

	// Create host key callback - conditional based on whether host_key is provided
	var hostKeyCallback ssh.HostKeyCallback

	if strings.TrimSpace(hostKeyData) == "" {
		// WARNING: No host_key provided - using InsecureIgnoreHostKey
		// This skips host key verification and is VULNERABLE to Man-in-the-Middle (MITM) attacks
		// For production use, always provide host_key in the secret to enable secure verification
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		// Use golang.org/x/crypto/ssh/knownhosts for production-grade known_hosts parsing
		// This properly handles:
		// - Hashed hostnames (|1|...|...)
		// - Wildcard patterns (*.example.com, !*.internal)
		// - Standard OpenSSH known_hosts formats

		// knownhosts.New() requires a file path, so create a temporary file
		// to hold the in-memory hostKeyData from the Kubernetes secret
		tmpFile, err := os.CreateTemp("", "known_hosts_*")
		if err != nil {
			return fmt.Errorf("failed to create temporary known_hosts file: %w", err)
		}
		tmpFilePath := tmpFile.Name()
		defer os.Remove(tmpFilePath) // Clean up temp file

		// Write the known_hosts data to the temporary file
		if _, err := tmpFile.WriteString(hostKeyData); err != nil {
			tmpFile.Close()
			return fmt.Errorf("failed to write known_hosts data to temporary file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			return fmt.Errorf("failed to close temporary known_hosts file: %w", err)
		}

		// Use knownhosts.New() to get a proper callback with full OpenSSH support
		hostKeyCallback, err = knownhosts.New(tmpFilePath)
		if err != nil {
			return fmt.Errorf("failed to parse known_hosts data: %w", err)
		}
	}

	// Configure the SSH client with proper host key verification
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
			return fmt.Errorf("SFTP connection failed: %w", result.err)
		}
		conn = result.conn
	case <-ctx.Done():
		// Context cancelled - connection will be abandoned
		return fmt.Errorf("SFTP connection cancelled: %w", ctx.Err())
	}
	defer conn.Close()

	// Attempt to create an SFTP client
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	// Success - connection and authentication worked
	return nil
}

// containsPort checks if the host string already contains a port.
func containsPort(host string) bool {
	_, _, err := net.SplitHostPort(host)
	// If SplitHostPort succeeds, a valid host:port was present
	return err == nil
}
