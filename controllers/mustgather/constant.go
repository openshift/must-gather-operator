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

	// gatherSuccessMarkerPath is the path to the marker file that the gather container
	// writes on successful completion. The upload container checks for this file before
	// proceeding with obfuscation or SFTP upload.
	gatherSuccessMarkerPath = "/must-gather/.gather-success"

	// obfuscateChownSuffix transfers gather output ownership to the upload container UID (65534).
	// Captures the gather exit status first, writes the success marker on zero exit,
	// runs chown (|| true so non-root images don't cause retries), then exits with
	// the original status so gather failures propagate.
	obfuscateChownSuffix = "gather_rc=$?; if [ $gather_rc -eq 0 ]; then touch " + gatherSuccessMarkerPath + "; fi; chown -R 65534:65534 /must-gather || true; exit $gather_rc"
)
