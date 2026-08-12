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

	// Obfuscation env vars consumed by build/bin/upload.
	obfuscateEnvEnabled = "obfuscate"
	obfuscateEnvConfig  = "obfuscate_config"

	// Obfuscation custom ConfigMap volume/mount paths.
	obfuscateConfigVolumeName = "obfuscate-config"
	obfuscateConfigMountPath  = "/etc/must-gather-clean/custom-config/config.yaml"
	obfuscateConfigMapKey     = "config.yaml"

	// obfuscateChownSuffix transfers gather output ownership to the upload container UID (65534).
	// Captures the gather exit status first, runs chown (|| true so non-root images don't
	// cause retries), then exits with the original status so gather failures propagate.
	obfuscateChownSuffix = "gather_rc=$?; chown -R 65534:65534 /must-gather || true; exit $gather_rc"
)
