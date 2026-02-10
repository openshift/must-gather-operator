package mustgather

// Constants defining the supported transfer protocols and validation types
const (
	// ValidationServiceAccount represents the validation type for Service account
	ValidationServiceAccount = "Service Account"
  
	// ProtocolSFTP represents the SFTP (SSH File Transfer Protocol)
	ProtocolSFTP = "SFTP"

	// ProtocolTCP represents the TCP protocol
	ProtocolTCP = "tcp"

	// ValidationSFTPCredentials represents the validation type for SFTP credentials
	ValidationSFTPCredentials = "SFTP credentials"

	// MaxSFTPValidationRetries is the maximum number of retries for transient SFTP validation errors
	MaxSFTPValidationRetries = 3

	// ValidationImageStream represents the validation type for ImageStream
	ValidationImageStream = "ImageStream"

	// DefaultMustGatherImageEnv represents the environment variable for the default must-gather image
	DefaultMustGatherImageEnv = "DEFAULT_MUST_GATHER_IMAGE"
)
