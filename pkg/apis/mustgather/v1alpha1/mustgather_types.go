package v1alpha1

import (
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MustGatherSpec defines the desired state of MustGather
// +k8s:openapi-gen=true
type MustGatherSpec struct {
	// The is of the case this must gather will be uploaded to
	// +kubebuilder:validation:Required
	CaseID string `json:"caseID"`

	// the secret container a username and password field to be used to authenticate with red hat case management systems
	// +kubebuilder:validation:Required
	CaseManagementAccountSecretRef corev1.LocalObjectReference `json:"caseManagementAccountSecretRef"`

	// the service account to use to run the must gather job pod, defaults to default
	// +kubebuilder:validation:Optional
	/* +kubebuilder:default:="{Name:default}" */
	ServiceAccountRef corev1.LocalObjectReference `json:"serviceAccountRef,omitempty"`

	// The list of must gather images to run, optional, it will default to: $DEFAULT_MUST_GATHER_IMAGE
	// +kubebuilder:validation:Optional
	// +listType=set
	MustGatherImages []string `json:"mustGatherImages,omitempty"`

	// This represents the proxy configuration to be used. If left empty it will default to the cluster-level proxy configuration.
	// +kubebuilder:validation:Optional
	ProxyConfig ProxySpec `json:"proxyConfig,omitempty"`
}

// +k8s:openapi-gen=true
type ProxySpec struct {
	// httpProxy is the URL of the proxy for HTTP requests.  Empty means unset and will not result in an env var.
	// +optional
	HTTPProxy string `json:"httpProxy,omitempty"`

	// httpsProxy is the URL of the proxy for HTTPS requests.  Empty means unset and will not result in an env var.
	// +optional
	HTTPSProxy string `json:"httpsProxy,omitempty"`

	// noProxy is the list of domains for which the proxy should not be used.  Empty means unset and will not result in an env var.
	// +optional
	NoProxy string `json:"noProxy,omitempty"`
}

// MustGatherStatus defines the observed state of MustGather
// +k8s:openapi-gen=true
type MustGatherStatus struct {
	// +kubebuilder:validation:Enum="Success";"Failure"
	Status     string      `json:"status,omitempty"`
	LastUpdate metav1.Time `json:"lastUpdate,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	Completed  bool        `json:"completed"`
}

func (m *MustGather) GetReconcileStatus() apis.ReconcileStatus {
	return apis.ReconcileStatus{
		Status:     m.Status.Status,
		LastUpdate: m.Status.LastUpdate,
		Reason:     m.Status.Reason,
	}
}

func (m *MustGather) SetReconcileStatus(reconcileStatus apis.ReconcileStatus) {
	m.Status.Status = reconcileStatus.Status
	m.Status.LastUpdate = reconcileStatus.LastUpdate
	m.Status.Reason = reconcileStatus.Reason
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MustGather is the Schema for the mustgathers API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=mustgathers,scope=Namespaced
type MustGather struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MustGatherSpec   `json:"spec,omitempty"`
	Status MustGatherStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MustGatherList contains a list of MustGather
type MustGatherList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MustGather `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MustGather{}, &MustGatherList{})
}
