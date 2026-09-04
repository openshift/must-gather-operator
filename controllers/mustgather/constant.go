package mustgather

// Constants defining the supported transfer protocols and validation types
const (
	// ValidationServiceAccount represents the validation type for Service account
	ValidationServiceAccount = "Service Account"

	// ProtocolSFTP represents the SFTP (SSH File Transfer Protocol)
	ProtocolSFTP = "SFTP"

	// sftpValidationFailedUserMessage is written to MustGather status and events
	// when SFTP connection validation fails. Network-level details must not
	// appear in CR status because they can be used as a reachability oracle
	// against CR-supplied hosts.
	sftpValidationFailedUserMessage = "unable to connect to the SFTP server"

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

	// gatherExitCodeFile is the marker file the gather container writes its exit
	// code to. The upload container reads it to decide whether to proceed.
	gatherExitCodeFile = "/must-gather/.gather-exit-code"

	// gatherExitSuffix writes the gather exit code to the marker file and exits.
	// Appended to gatherCommand (default path) or custom-command wrappers
	// when an upload container is present.
	// Expects $gather_rc to be set by the preceding script.
	gatherExitSuffix = "\necho $gather_rc > " + gatherExitCodeFile + "\nexit $gather_rc"

	// obfuscateChownSuffix transfers gather output ownership to the upload container UID (65534).
	// Writes the exit code marker, runs chown (|| true so non-root images don't
	// cause retries), then exits with the original status so gather failures propagate.
	// Expects $gather_rc to be set by the preceding script.
	obfuscateChownSuffix = "\necho $gather_rc > " + gatherExitCodeFile + "\nchown -R 65534:65534 /must-gather || true\nexit $gather_rc"
)
