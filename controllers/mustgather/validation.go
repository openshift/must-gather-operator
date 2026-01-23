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
	// Timeout for individual SSH dial operations
	sshDialTimeout = 5 * time.Second

	// Default SFTP port
	sftpDefaultPort = "22"
)

// sftpDialFunc is the function used to test SFTP connections.
// It can be overridden in tests to avoid real network calls.
var sftpDialFunc = checkSFTPConnection

// sshDialFunc is the function used to dial SSH connections.
// It can be overridden in tests to avoid real network calls.
// The signature matches ssh.Dial for easy stubbing.
var sshDialFunc = func(network, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	return ssh.Dial(network, addr, config)
}

// IsTransientError checks if an error is transient and should trigger a requeue
func IsTransientError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}

	return false
}

// validateSFTPCredentials tests SFTP connection with the provided credentials.
//
// The username and password parameters must be non-empty (validated by caller).
// The host parameter is expected to be non-empty (enforced by CRD default value).
//
// It returns an error if:
// - Connection to SFTP host fails
// - Authentication with provided credentials fails
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
	// Call dial function directly - SSH config timeout will handle timing out the connection
	return sftpDialFunc(username, password, host)
}

// checkSFTPConnection attempts to establish an SFTP connection and authenticate.
// This is a lightweight test that only checks credentials without transferring files.
//
// The host parameter accepts:
// - IPv4: "hostname" or "hostname:port" or "192.0.2.1" or "192.0.2.1:2222"
// - IPv6: "[2001:db8::1]" or "[2001:db8::1]:2222" or "2001:db8::1" (auto-bracketed)
// If no port is specified, the default SFTP port (22) is used.
//
// The SSH connection timeout is controlled by sshDialTimeout (5 seconds).
func checkSFTPConnection(username, password, host string) error {
	address := normalizeHostAddress(host)

	// Skip host key verification to match upload script (StrictHostKeyChecking=no)
	// #nosec G106 -- Intentional: matches upload script behavior
	config := buildSSHConfig(username, password, ssh.InsecureIgnoreHostKey())
	conn, err := sshDialFunc(ProtocolTCP, address, config)
	if err != nil {
		return fmt.Errorf("SFTP connection failed: %w", err)
	}
	defer conn.Close()

	return verifySFTPSubsystem(conn)
}

// normalizeHostAddress adds the default SFTP port if not specified.
// It handles both IPv4 and IPv6 addresses, ensuring IPv6 addresses are correctly bracketed.
func normalizeHostAddress(host string) string {
	if host == "" || containsPort(host) {
		return host
	}

	// If the host is already bracketed (IPv6), strip brackets so JoinHostPort can re-add them correctly
	// along with the port. JoinHostPort automatically brackets IPv6 literals.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	return net.JoinHostPort(host, sftpDefaultPort)
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

// verifySFTPSubsystem verifies that the SFTP subsystem is available on the SSH connection.
// This creates and immediately closes an SFTP client to confirm functionality.
func verifySFTPSubsystem(conn *ssh.Client) error {
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

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
