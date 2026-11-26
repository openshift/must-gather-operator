/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mustgather

import (
	"fmt"
	"slices"
	"strings"

	mustgatherv1 "github.com/openshift/must-gather-operator/api/v1"
)

const (
	// ValidationSFTPHostAllowlist is the validation type for SFTP host allowlist checks.
	ValidationSFTPHostAllowlist = "SFTP host allowlist"

	sftpHostNotPermittedMessage = "SFTP host is not permitted by MustGatherPolicy"
)

// effectiveSFTPHost returns the SFTP hostname that will be used for upload,
// applying the CRD default when the MustGather leaves host unset.
func effectiveSFTPHost(sftp *mustgatherv1.SFTPSpec) string {
	if sftp == nil {
		return mustgatherv1.DefaultSFTPHost
	}
	host := strings.TrimSpace(derefString(sftp.Host))
	if host == "" {
		return mustgatherv1.DefaultSFTPHost
	}
	return host
}

// validateSFTPHostAllowlist checks the MustGather SFTP host against
// MustGatherPolicy.spec.uploadTargets when that field is set.
//
// Semantics:
//   - policy == nil or uploadTargets unset: no constraint (today's behavior).
//   - uploadTargets set, SFTP not in allowedTypes: reject.
//   - SFTP allowed, sftp omitted or allowedHosts empty: reject.
//   - allowedHosts non-empty: effective host must match exactly.
func validateSFTPHostAllowlist(policy *mustgatherv1.MustGatherPolicy, host string) error {
	if policy == nil || policy.Spec.UploadTargets == nil {
		return nil
	}

	uploadTargets := policy.Spec.UploadTargets
	if !slices.Contains(uploadTargets.AllowedTypes, mustgatherv1.UploadTypeSFTP) {
		return fmt.Errorf("%s: SFTP is not listed in allowedTypes", sftpHostNotPermittedMessage)
	}

	if uploadTargets.SFTP == nil || len(uploadTargets.SFTP.AllowedHosts) == 0 {
		return fmt.Errorf("%s: no SFTP hosts are permitted by policy", sftpHostNotPermittedMessage)
	}

	if !slices.Contains(uploadTargets.SFTP.AllowedHosts, host) {
		return fmt.Errorf("%s: %q is not in allowedHosts", sftpHostNotPermittedMessage, host)
	}

	return nil
}
