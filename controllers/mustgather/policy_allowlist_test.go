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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	mustgatherv1 "github.com/openshift/must-gather-operator/api/v1"
	"k8s.io/utils/ptr"
)

func TestEffectiveSFTPHost(t *testing.T) {
	tests := []struct {
		name string
		sftp *mustgatherv1.SFTPSpec
		want string
	}{
		{name: "nil_sftp", sftp: nil, want: mustgatherv1.DefaultSFTPHost},
		{name: "nil_host", sftp: &mustgatherv1.SFTPSpec{}, want: mustgatherv1.DefaultSFTPHost},
		{name: "empty_host", sftp: &mustgatherv1.SFTPSpec{Host: ptr.To("")}, want: mustgatherv1.DefaultSFTPHost},
		{name: "whitespace_host", sftp: &mustgatherv1.SFTPSpec{Host: ptr.To("  ")}, want: mustgatherv1.DefaultSFTPHost},
		{name: "custom_host", sftp: &mustgatherv1.SFTPSpec{Host: ptr.To("sftp.example.com")}, want: "sftp.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveSFTPHost(tt.sftp); got != tt.want {
				t.Fatalf("effectiveSFTPHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultSFTPHostMatchesCRDDefault keeps DefaultSFTPHost and the generated
// MustGather CRD host default from drifting. Kubebuilder markers cannot
// reference Go constants, so this is the CI coupling.
func TestDefaultSFTPHostMatchesCRDDefault(t *testing.T) {
	const want = "sftp.access.redhat.com"
	if mustgatherv1.DefaultSFTPHost != want {
		t.Fatalf("DefaultSFTPHost = %q, want %q (update SFTPSpec.Host +default and regenerate CRDs together)", mustgatherv1.DefaultSFTPHost, want)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../.."))
	crdPath := filepath.Join(repoRoot, "deploy/crds/operator.openshift.io_mustgathers.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("read MustGather CRD: %v", err)
	}

	// Match the OpenAPI default on uploadTarget.sftp.host (appears for v1 and served versions).
	re := regexp.MustCompile(`(?m)^\s+host:\n\s+default:\s+(\S+)\n`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no host default found in %s", crdPath)
	}
	for _, m := range matches {
		if got := m[1]; got != mustgatherv1.DefaultSFTPHost {
			t.Fatalf("MustGather CRD host default = %q, want DefaultSFTPHost %q in %s", got, mustgatherv1.DefaultSFTPHost, crdPath)
		}
	}
}

func TestValidateSFTPHostAllowlist(t *testing.T) {
	policyWith := func(mut func(*mustgatherv1.MustGatherPolicy)) *mustgatherv1.MustGatherPolicy {
		p := &mustgatherv1.MustGatherPolicy{}
		mut(p)
		return p
	}

	tests := []struct {
		name        string
		policy      *mustgatherv1.MustGatherPolicy
		host        string
		wantErr     bool
		errContains string
	}{
		{
			name:    "nil_policy_allows",
			policy:  nil,
			host:    "sftp.example.com",
			wantErr: false,
		},
		{
			name:    "omitted_upload_targets_allows",
			policy:  policyWith(func(*mustgatherv1.MustGatherPolicy) {}),
			host:    "sftp.example.com",
			wantErr: false,
		},
		{
			name: "sftp_not_in_allowed_types",
			policy: policyWith(func(p *mustgatherv1.MustGatherPolicy) {
				p.Spec.UploadTargets = &mustgatherv1.UploadTargetPolicy{
					AllowedTypes: []mustgatherv1.UploadType{},
				}
			}),
			host:        mustgatherv1.DefaultSFTPHost,
			wantErr:     true,
			errContains: "not listed in allowedTypes",
		},
		{
			name: "sftp_allowed_but_sftp_block_omitted",
			policy: policyWith(func(p *mustgatherv1.MustGatherPolicy) {
				p.Spec.UploadTargets = &mustgatherv1.UploadTargetPolicy{
					AllowedTypes: []mustgatherv1.UploadType{mustgatherv1.UploadTypeSFTP},
				}
			}),
			host:        mustgatherv1.DefaultSFTPHost,
			wantErr:     true,
			errContains: "no SFTP hosts are permitted",
		},
		{
			name: "sftp_allowed_but_allowed_hosts_empty",
			policy: policyWith(func(p *mustgatherv1.MustGatherPolicy) {
				p.Spec.UploadTargets = &mustgatherv1.UploadTargetPolicy{
					AllowedTypes: []mustgatherv1.UploadType{mustgatherv1.UploadTypeSFTP},
					SFTP:         &mustgatherv1.SFTPUploadTargetPolicy{AllowedHosts: []string{}},
				}
			}),
			host:        mustgatherv1.DefaultSFTPHost,
			wantErr:     true,
			errContains: "no SFTP hosts are permitted",
		},
		{
			name: "host_not_in_allowlist",
			policy: policyWith(func(p *mustgatherv1.MustGatherPolicy) {
				p.Spec.UploadTargets = &mustgatherv1.UploadTargetPolicy{
					AllowedTypes: []mustgatherv1.UploadType{mustgatherv1.UploadTypeSFTP},
					SFTP: &mustgatherv1.SFTPUploadTargetPolicy{
						AllowedHosts: []string{mustgatherv1.DefaultSFTPHost},
					},
				}
			}),
			host:        "disallowed.sftp.example.com",
			wantErr:     true,
			errContains: "not in allowedHosts",
		},
		{
			name: "host_in_allowlist",
			policy: policyWith(func(p *mustgatherv1.MustGatherPolicy) {
				p.Spec.UploadTargets = &mustgatherv1.UploadTargetPolicy{
					AllowedTypes: []mustgatherv1.UploadType{mustgatherv1.UploadTypeSFTP},
					SFTP: &mustgatherv1.SFTPUploadTargetPolicy{
						AllowedHosts: []string{mustgatherv1.DefaultSFTPHost, "sftp.example.com"},
					},
				}
			}),
			host:    "sftp.example.com",
			wantErr: false,
		},
		{
			name: "default_host_in_allowlist",
			policy: policyWith(func(p *mustgatherv1.MustGatherPolicy) {
				p.Spec.UploadTargets = &mustgatherv1.UploadTargetPolicy{
					AllowedTypes: []mustgatherv1.UploadType{mustgatherv1.UploadTypeSFTP},
					SFTP: &mustgatherv1.SFTPUploadTargetPolicy{
						AllowedHosts: []string{mustgatherv1.DefaultSFTPHost},
					},
				}
			}),
			host:    mustgatherv1.DefaultSFTPHost,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSFTPHostAllowlist(tt.policy, tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
