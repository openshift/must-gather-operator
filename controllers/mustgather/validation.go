package mustgather

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
)

const (
	// Timeout for SFTP connection validation
	sftpValidationTimeout = 10 * time.Second

	// Default SFTP port
	sftpDefaultPort = "22"
)

// validateSFTPCredentials tests SFTP connection with the provided credentials.
// It returns an error if:
// - The secret doesn't exist or is missing required fields
// - Connection to SFTP host fails
// - Authentication with provided credentials fails
func validateSFTPCredentials(
	ctx context.Context,
	k8sClient client.Client,
	secretRef corev1.LocalObjectReference,
	host string,
	namespace string,
) error {
	// Retrieve the secret
	secret := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      secretRef.Name,
	}, secret)
	if err != nil {
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

	// Test SFTP connection with timeout
	validationCtx, cancel := context.WithTimeout(ctx, sftpValidationTimeout)
	defer cancel()

	// Run validation in a goroutine to respect context timeout
	errChan := make(chan error, 1)
	go func() {
		errChan <- testSFTPConnection(string(username), string(password), host)
	}()

	select {
	case err := <-errChan:
		return err
	case <-validationCtx.Done():
		return fmt.Errorf("SFTP credential validation timed out after %v", sftpValidationTimeout)
	}
}

// testSFTPConnection attempts to establish an SFTP connection and authenticate.
// This is a lightweight test that only checks credentials without transferring files.
func testSFTPConnection(username, password, host string) error {
	// Add default port if not specified
	address := host
	if address != "" && address[len(address)-1] != ':' && !containsPort(address) {
		address = fmt.Sprintf("%s:%s", address, sftpDefaultPort)
	}

	// Configure SSH client
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		// Note: In production, you should verify host keys properly
		// For now, we accept any host key for validation purposes
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	// Attempt SSH connection
	conn, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("SFTP authentication failed: %w", err)
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

// updateStatusWithValidationError updates the MustGather status to indicate validation failure
func (r *MustGatherReconciler) updateStatusWithValidationError(
	ctx context.Context,
	instance *mustgatherv1alpha1.MustGather,
	validationType string,
	err error,
) error {
	instance.Status.Status = "Failed"
	instance.Status.Completed = true
	instance.Status.Reason = fmt.Sprintf("%s validation failed: %v", validationType, err)

	// Update the status
	if statusErr := r.GetClient().Status().Update(ctx, instance); statusErr != nil {
		return fmt.Errorf("failed to update MustGather status: %w", statusErr)
	}

	return nil
}
