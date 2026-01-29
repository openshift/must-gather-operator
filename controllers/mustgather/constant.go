package mustgather

// Constants defining the supported transfer protocols and validation types
const (
	// ProtocolSFTP represents the SFTP (SSH File Transfer Protocol)
	ProtocolSFTP = "SFTP"

	// ProtocolTCP represents the TCP protocol
	ProtocolTCP = "tcp"

	// ValidationSFTPCredentials represents the validation type for SFTP credentials
	ValidationSFTPCredentials = "SFTP credentials"

	// SFTPValidationRetryAnnotation is the annotation key used to track SFTP validation retry count
	SFTPValidationRetryAnnotation = "mustgather.openshift.io/sftp-validation-retries"

	// MaxSFTPValidationRetries is the maximum number of retries for transient SFTP validation errors
	MaxSFTPValidationRetries = 3
)
