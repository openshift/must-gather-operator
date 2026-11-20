/*
Copyright 2022.

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
	"context"
	goerror "errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
	imagev1 "github.com/openshift/api/image/v1"
	mustgatherv1 "github.com/openshift/must-gather-operator/api/v1"
	"github.com/openshift/must-gather-operator/pkg/localmetrics"
	"github.com/openshift/must-gather-operator/pkg/mustgatherutil"
	reconcilers "reconciler.io/runtime/reconcilers"
	rtime "reconciler.io/runtime/time"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "mustgather-controller"

	// default namespace is always present
	DefaultMustGatherNamespace = "default"
)

var log = logf.Log.WithName(ControllerName)

// blank assignment to verify that MustGatherReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &MustGatherReconciler{}

const (
	mustGatherFinalizer = "finalizer.mustgathers.operator.openshift.io"

	// Condition types/reasons recorded on the MustGather status for each
	// reconcile cycle (values match the historical operator-utils conditions).
	conditionReconcileSuccess = "ReconcileSuccess"
	conditionReconcileError   = "ReconcileError"
	reasonReconcileSuccess    = "LastReconcileCycleSucceded"
	reasonReconcileError      = "LastReconcileCycleFailed"
)

var errImageValidation = goerror.New("image validation failed")

// MustGatherReconciler reconciles a MustGather object by composing
// reconciler.io/runtime sub-reconcilers instead of a monolithic Reconcile
// method:
//
//	ResourceReconciler  fetches the CR, requeues on error and persists status changes
//	└─ Advice           records ReconcileSuccess/ReconcileError conditions for each cycle
//	   └─ WithFinalizer adds/clears the MustGather finalizer
//	      └─ Sequence
//	         ├─ ValidateServiceAccount       rejects the operator's own SA
//	         ├─ SyncTrustedCAConfigMap       replicates the trusted CA bundle
//	         ├─ ValidateObfuscationConfigMap requires the obfuscation ConfigMap
//	         └─ GatherJob                    creates/observes the Job, cleans up on
//	                                           completion and on CR deletion (Finalize)
//
// Terminal validation failures set a Failed status and return
// reconcilers.ErrHaltSubReconcilers, which stops the sequence without
// requeueing (the spec is immutable, so retrying is pointless). Transient
// errors are returned as errors and requeued with backoff.
type MustGatherReconciler struct {
	// TrustedCAConfigMap is the name of the ConfigMap containing the trusted CA certificate bundle
	TrustedCAConfigMap string
	// OperatorNamespace is the namespace where the operator is running
	OperatorNamespace string
	// DefaultMustGatherImage is the default must-gather image
	DefaultMustGatherImage string
	// OperatorServiceAccountName is the SA the operator pod runs as, discovered at startup
	OperatorServiceAccountName string

	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	composeOnce sync.Once
	composed    *reconcilers.ResourceReconciler[*mustgatherv1.MustGather]
}

//+kubebuilder:rbac:groups=operator.openshift.io,resources=mustgathers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=mustgathers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.openshift.io,resources=mustgathers/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs;jobs/finalizers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;create
//+kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/finalizers,verbs=update
//+kubebuilder:rbac:groups=image.openshift.io,resources=imagestreams,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods;services;services/finalizers;endpoints;persistentvolumeclaims;events;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;delete
// ServiceAccount read access needed for pre-flight validation before Job creation
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicies,verbs=create
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicies,resourceNames=block-mustgather-restricted-namespaces,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicybindings,verbs=create
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicybindings,resourceNames=block-mustgather-restricted-namespaces,verbs=get;list;watch;update;patch

// Reconcile moves the current state of the cluster closer to the desired
// state. The work is delegated to the composed reconciler.io/runtime tree.
func (r *MustGatherReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	r.composeOnce.Do(func() { r.composed = r.newResourceReconciler() })
	return r.composed.Reconcile(ctx, request)
}

// GetClient returns the underlying client used by this reconciler.
func (r *MustGatherReconciler) GetClient() client.Client {
	return r.Client
}

// SetupWithManager sets up the controller with the Manager. The controller is
// wired with the controller-runtime builder directly (instead of the
// ResourceReconciler's own SetupWithManager) so the primary watch can carry
// the generation/finalizer predicate: reconciler.io/runtime v0.23 registers
// For() without options, and ForOption support (SetupForOptions) only landed
// in v0.25, which requires controller-runtime >= v0.23.
func (r *MustGatherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.composeOnce.Do(func() { r.composed = r.newResourceReconciler() })
	rr := r.composed

	ctx := logf.IntoContext(context.Background(), log)
	ctx = reconcilers.StashConfig(ctx, rr.Config)
	ctx = reconcilers.StashOriginalConfig(ctx, rr.Config)
	ctx = reconcilers.StashResourceType(ctx, rr.Type)
	ctx = reconcilers.StashOriginalResourceType(ctx, rr.Type)

	if err := rr.Validate(ctx); err != nil {
		return err
	}

	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&mustgatherv1.MustGather{}, builder.WithPredicates(resourceGenerationOrFinalizerChangedPredicate())).
		Owns(&batchv1.Job{}, builder.WithPredicates(isStateUpdated()))

	if r.TrustedCAConfigMap != "" {
		bldr = bldr.Owns(&corev1.ConfigMap{}, builder.WithPredicates(isNameEquals(r.TrustedCAConfigMap)))
	}

	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return r.ensureAdmissionPolicy(ctx, mgr.GetClient(), mgr.GetAPIReader())
	})); err != nil {
		return fmt.Errorf("failed to add admission policy runnable: %w", err)
	}

	if err := rr.Reconciler.SetupWithManager(ctx, mgr, bldr); err != nil {
		return err
	}
	_, err := bldr.Build(rr)
	return err
}

// newResourceReconciler composes the reconciler.io/runtime tree for the
// MustGather lifecycle. See the MustGatherReconciler doc comment.
func (r *MustGatherReconciler) newResourceReconciler() *reconcilers.ResourceReconciler[*mustgatherv1.MustGather] {
	return &reconcilers.ResourceReconciler[*mustgatherv1.MustGather]{
		Name:   "MustGather",
		Type:   &mustgatherv1.MustGather{},
		Config: reconcilers.Config{Client: r.Client, Recorder: r.Recorder},
		Reconciler: &reconcilers.Advice[*mustgatherv1.MustGather]{
			Name: "ManageOutcome",
			Reconciler: &reconcilers.WithFinalizer[*mustgatherv1.MustGather]{
				Finalizer: mustGatherFinalizer,
				Reconciler: reconcilers.Sequence[*mustgatherv1.MustGather]{
					&reconcilers.SyncReconciler[*mustgatherv1.MustGather]{
						Name: "ValidateServiceAccount",
						Sync: r.validateServiceAccount,
					},
					&reconcilers.SyncReconciler[*mustgatherv1.MustGather]{
						Name: "SyncTrustedCAConfigMap",
						Sync: r.syncTrustedCAConfigMap,
					},
					&reconcilers.SyncReconciler[*mustgatherv1.MustGather]{
						Name: "ValidateObfuscationConfigMap",
						Sync: r.validateObfuscationConfigMap,
					},
					&reconcilers.SyncReconciler[*mustgatherv1.MustGather]{
						Name:     "GatherJob",
						Sync:     r.syncGatherJob,
						Finalize: r.finalizeGatherJob,
					},
				},
			},
			After: r.manageOutcome,
		},
	}
}

// manageOutcome records the outcome of a reconcile cycle on the MustGather
// status, mirroring the historical operator-utils ManageError/ManageSuccess
// conditions: errors surface as a ReconcileError condition plus a warning
// event, successful cycles as a ReconcileSuccess condition. Terminal
// validation failures (ErrHaltSubReconcilers) and finalization keep the
// status untouched.
func (r *MustGatherReconciler) manageOutcome(ctx context.Context, instance *mustgatherv1.MustGather, result reconcile.Result, err error) (reconcile.Result, error) {
	switch {
	case err == nil && instance.GetDeletionTimestamp() == nil:
		setReconcileCondition(ctx, instance, metav1.Condition{
			Type:               conditionReconcileSuccess,
			Status:             metav1.ConditionTrue,
			Reason:             reasonReconcileSuccess,
			ObservedGeneration: instance.GetGeneration(),
		})
	case err != nil && !goerror.Is(err, reconcilers.ErrHaltSubReconcilers) && instance.GetDeletionTimestamp() == nil:
		setReconcileCondition(ctx, instance, metav1.Condition{
			Type:               conditionReconcileError,
			Status:             metav1.ConditionTrue,
			Reason:             reasonReconcileError,
			Message:            err.Error(),
			ObservedGeneration: instance.GetGeneration(),
		})
		r.Recorder.Event(instance, corev1.EventTypeWarning, "ProcessingError", err.Error())
	}
	return result, err
}

func setReconcileCondition(ctx context.Context, instance *mustgatherv1.MustGather, condition metav1.Condition) {
	if instance.Status == nil {
		// status.completed is a required field; initialize it so the first
		// status write is not rejected by the API server.
		instance.Status = &mustgatherv1.MustGatherStatus{Completed: ptr.To(false)}
	}
	condition.LastTransitionTime = metav1.NewTime(rtime.RetrieveNow(ctx))
	apimeta.SetStatusCondition(&instance.Status.Conditions, condition)
}

// validateServiceAccount rejects the operator's own service account before
// any other validation. This runs on every reconcile (even if a Job already
// exists) so that pre-existing CRs created before this check was introduced
// are flagged.
func (r *MustGatherReconciler) validateServiceAccount(ctx context.Context, instance *mustgatherv1.MustGather) error {
	saName := instance.Spec.ServiceAccountName

	if instance.Namespace == r.OperatorNamespace && saName == r.OperatorServiceAccountName {
		validationErr := fmt.Errorf("serviceAccountName %q is not allowed in namespace %q: the operator's own service account cannot be used for must-gather jobs", saName, instance.Namespace)
		logr.FromContextOrDiscard(ctx).Error(validationErr, "operator service account usage rejected",
			"name", saName, "namespace", instance.Namespace, "operatorNamespace", r.OperatorNamespace)
		return r.setValidationFailureStatus(ctx, instance, ValidationServiceAccount, validationErr)
	}

	return nil
}

// syncTrustedCAConfigMap copies the trusted CA ConfigMap from the operator
// namespace into the CR namespace, when configured.
func (r *MustGatherReconciler) syncTrustedCAConfigMap(ctx context.Context, instance *mustgatherv1.MustGather) error {
	if r.TrustedCAConfigMap == "" {
		return nil
	}
	return r.ensureTrustedCAConfigMap(ctx, logr.FromContextOrDiscard(ctx), instance)
}

// validateObfuscationConfigMap verifies that the ConfigMap referenced for
// obfuscation exists in the CR namespace before a Job is created.
func (r *MustGatherReconciler) validateObfuscationConfigMap(ctx context.Context, instance *mustgatherv1.MustGather) error {
	cmName := getObfuscateConfigMapRefName(instance.Spec.Obfuscate)
	if cmName == "" {
		return nil
	}
	cm := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: instance.Namespace}, cm); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "obfuscation ConfigMap not found", "configMapName", cmName, "namespace", instance.Namespace)
		return fmt.Errorf("obfuscation ConfigMap %q not found in namespace %q: %w", cmName, instance.Namespace, err)
	}
	return nil
}

// syncGatherJob observes the gather Job owned by the MustGather CR, creating
// it after validation when it does not exist yet, and reflecting completion on
// the CR status.
func (r *MustGatherReconciler) syncGatherJob(ctx context.Context, instance *mustgatherv1.MustGather) error {
	reqLogger := logr.FromContextOrDiscard(ctx)

	existingJob := &batchv1.Job{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      instance.Name,
		Namespace: instance.Namespace,
	}, existingJob)

	if err != nil {
		if !errors.IsNotFound(err) {
			reqLogger.Error(err, "unable to look up", "job", types.NamespacedName{
				Name:      instance.Name,
				Namespace: instance.Namespace,
			})
			return err
		}

		// Job not found — build the template and validate before creating
		return r.createGatherJob(ctx, reqLogger, instance)
	}

	// Check status of job and update any metric counts
	if existingJob.Status.Active > 0 {
		reqLogger.Info("mustgather Job pods are still running")
	} else {
		// if the job has been marked as Succeeded or Failed but instance has no DeletionTimestamp,
		// handle resource clean-up (delete secret, job, and MustGather)
		if existingJob.Status.Succeeded > 0 {
			reqLogger.Info("mustgather Job pods succeeded")
			return r.handleJobCompletion(ctx, reqLogger, instance, "Completed", "MustGather Job pods succeeded")
		}
		backoffLimit := int32(0)
		if existingJob.Spec.BackoffLimit != nil {
			backoffLimit = *existingJob.Spec.BackoffLimit
		}
		if existingJob.Status.Failed > backoffLimit {
			reqLogger.Info("MustGather Job pods failed")
			localmetrics.MetricMustGatherErrors.Inc()
			reason := r.buildJobFailureReason(ctx, reqLogger, existingJob)
			return r.handleJobCompletion(ctx, reqLogger, instance, "Failed", reason)
		}
	}

	// if we get here it means that either
	// 1. the mustgather instance was updated, which we don't support and we are going to ignore
	// 2. the job was updated, probably the status piece. we should the update the status of the instance, not supported yet.

	r.updateStatus(instance, existingJob)
	return nil
}

// finalizeGatherJob cleans up the Job and its Pods when the MustGather CR is
// deleted, unless resources are retained on completion.
func (r *MustGatherReconciler) finalizeGatherJob(ctx context.Context, instance *mustgatherv1.MustGather) error {
	if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
		return r.cleanupMustGatherResources(ctx, logr.FromContextOrDiscard(ctx), instance)
	}
	return nil
}

// createGatherJob builds the Job for the MustGather CR, validates all
// preconditions (image, ServiceAccount, SFTP credentials) and creates it.
func (r *MustGatherReconciler) createGatherJob(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather) error {
	job, err := r.getJobFromInstance(ctx, reqLogger, instance)
	if err != nil {
		if goerror.Is(err, errImageValidation) {
			return r.setValidationFailureStatus(ctx, instance, ValidationImageStream, err)
		}
		reqLogger.Error(err, "unable to get job from instance", "name", instance.Name, "namespace", instance.Namespace)
		return err
	}

	saName := instance.Spec.ServiceAccountName

	// Validate that the ServiceAccount exists before creating the Job.
	serviceAccount := &corev1.ServiceAccount{}
	err = r.Client.Get(ctx, types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      saName,
	}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Error(err, "service account not found", "name", saName, "namespace", instance.Namespace)
			return r.setValidationFailureStatus(ctx, instance, ValidationServiceAccount, err)
		}

		reqLogger.Error(err, "failed to get service account (transient error, will retry)", "name", saName, "namespace", instance.Namespace)
		return err
	}

	// look up user secret and validate SFTP credentials before creating the job
	if err := r.validateSFTPCredentials(ctx, reqLogger, instance); err != nil {
		return err
	}

	// job is not there, create it, owned by the MustGather instance.
	if err := controllerutil.SetControllerReference(instance, job, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		reqLogger.Error(err, "unable to create", "job", job)
		return err
	}
	// Increment prometheus metrics for must gather total
	localmetrics.MetricMustGatherTotal.Inc()
	return nil
}

// validateSFTPCredentials looks up the case management secret referenced by
// the CR and verifies its credentials against the SFTP server. Terminal
// validation failures set a Failed status; lookup errors are returned as
// transient errors.
func (r *MustGatherReconciler) validateSFTPCredentials(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather) error {
	if instance.Spec.UploadTarget == nil || instance.Spec.UploadTarget.SFTP == nil ||
		instance.Spec.UploadTarget.SFTP.CaseManagementAccountSecretRef.Name == "" {
		return nil
	}

	secretName := instance.Spec.UploadTarget.SFTP.CaseManagementAccountSecretRef.Name
	userSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      secretName,
	}, userSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Error(err, "secret not found", "secret", secretName, "namespace", instance.Namespace)
			return fmt.Errorf("secret %s not found in namespace %s: Please create the secret referenced by caseManagementAccountSecretRef", secretName, instance.Namespace)
		}
		reqLogger.Error(err, "error getting secret", "secret", secretName)
		return err
	}

	// Validate and extract required credentials
	username, usernameExists := userSecret.Data["username"]
	password, passwordExists := userSecret.Data["password"]

	if !usernameExists || len(username) == 0 {
		validationErr := fmt.Errorf("sftp credentials secret %q is missing required field 'username'", secretName)
		reqLogger.Error(validationErr, "sftp credential validation failed")
		return r.setValidationFailureStatus(ctx, instance, ValidationSFTPCredentials, validationErr)
	}

	if !passwordExists || len(password) == 0 {
		validationErr := fmt.Errorf("sftp credentials secret %q is missing required field 'password'", secretName)
		reqLogger.Error(validationErr, "sftp credential validation failed")
		return r.setValidationFailureStatus(ctx, instance, ValidationSFTPCredentials, validationErr)
	}

	// Validate SFTP credentials before creating the job
	reqLogger.Info("Validating SFTP credentials before creating must-gather job")
	validationErr := validateSFTPWithRetry(
		ctx,
		reqLogger,
		string(username),
		string(password),
		derefString(instance.Spec.UploadTarget.SFTP.Host),
	)
	if validationErr != nil {
		reqLogger.Error(validationErr, "SFTP credential validation failed")
		return r.setValidationFailureStatus(ctx, instance, ProtocolSFTP, goerror.New(sftpValidationFailedUserMessage))
	}

	reqLogger.Info("SFTP credentials validated successfully")
	return nil
}

// handleJobCompletion reflects the terminal Job state on the MustGather status
// and, unless resources are retained, cleans up the Job and its Pods. The
// status is persisted before cleanup so a failed cleanup requeues without
// recreating the Job.
func (r *MustGatherReconciler) handleJobCompletion(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather, status string, reason string) error {
	if instance.Status == nil {
		instance.Status = &mustgatherv1.MustGatherStatus{}
	}
	instance.Status.Status = ptr.To(status)
	instance.Status.Completed = ptr.To(true)
	instance.Status.Reason = ptr.To(reason)
	err := r.Client.Status().Update(ctx, instance)
	if err != nil {
		reqLogger.Error(err, "unable to update instance", "instance", instance.Name)
		return err
	}

	if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
		err := r.cleanupMustGatherResources(ctx, reqLogger, instance)
		if err != nil {
			reqLogger.Error(err, "failed to cleanup MustGather resources")
			return err
		}
	}
	return nil
}

func (r *MustGatherReconciler) buildJobFailureReason(ctx context.Context, reqLogger logr.Logger, job *batchv1.Job) string {
	const fallback = "MustGather Job pods failed"

	podList := &corev1.PodList{}
	if err := r.Client.List(ctx, podList,
		client.InNamespace(job.Namespace),
		client.MatchingLabels{"job-name": job.Name},
	); err != nil {
		reqLogger.Error(err, "failed to list pods for failure reason, using fallback")
		return fallback
	}
	if len(podList.Items) == 0 {
		return fallback
	}

	sort.Slice(podList.Items, func(i, j int) bool {
		return podList.Items[i].CreationTimestamp.After(podList.Items[j].CreationTimestamp.Time)
	})
	pod := podList.Items[0]

	var uploadCode int32
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated == nil || cs.State.Terminated.ExitCode == 0 {
			continue
		}
		code := cs.State.Terminated.ExitCode
		switch cs.Name {
		case gatherContainerName:
			if cs.State.Terminated.Reason == "OOMKilled" {
				return fmt.Sprintf("gather failed (exit code %d)", code)
			}
			if code == 124 || code == 137 {
				return fmt.Sprintf("gather timed out (exit code %d)", code)
			}
			return fmt.Sprintf("gather failed (exit code %d)", code)
		case uploadContainerName:
			uploadCode = code
		}
	}
	if uploadCode != 0 {
		return fmt.Sprintf("upload failed (exit code %d)", uploadCode)
	}
	return fallback
}

// updateStatus reflects the current Job state on the MustGather status. The
// composed ResourceReconciler persists the change.
func (r *MustGatherReconciler) updateStatus(instance *mustgatherv1.MustGather, job *batchv1.Job) {
	if instance.Status == nil {
		instance.Status = &mustgatherv1.MustGatherStatus{}
	}
	instance.Status.Completed = ptr.To(!job.Status.CompletionTime.IsZero())
}

// setValidationFailureStatus updates the MustGather status to indicate a
// validation failure: status Failed, completed, reason and timestamp are set
// and a warning event is recorded. It returns reconcilers.ErrHaltSubReconcilers
// to stop the reconcile loop without requeueing. Callers must pass a
// user-safe validationErr: network-level details in SFTP connection failures
// are a reachability oracle and must not be written to status or events.
func (r *MustGatherReconciler) setValidationFailureStatus(
	ctx context.Context,
	instance *mustgatherv1.MustGather,
	validationType string,
	validationErr error,
) error {
	errorMessage := fmt.Sprintf("%s validation failed: %v", validationType, validationErr)

	if instance.Status == nil {
		instance.Status = &mustgatherv1.MustGatherStatus{}
	}
	instance.Status.Status = ptr.To("Failed")
	instance.Status.Completed = ptr.To(true)
	instance.Status.Reason = ptr.To(errorMessage)
	now := metav1.NewTime(rtime.RetrieveNow(ctx))
	instance.Status.LastUpdate = &now

	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               conditionReconcileError,
		Status:             metav1.ConditionTrue,
		Reason:             "ValidationFailed",
		Message:            errorMessage,
		ObservedGeneration: instance.GetGeneration(),
	})

	// Record a warning event for the validation failure
	r.Recorder.Event(instance, corev1.EventTypeWarning, "ProcessingError", errorMessage)

	if statusErr := r.Client.Status().Update(ctx, instance); statusErr != nil {
		logr.FromContextOrDiscard(ctx).Error(statusErr, "failed to update status after validation error")
		return statusErr
	}
	return reconcilers.ErrHaltSubReconcilers
}

func (r *MustGatherReconciler) getJobFromInstance(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather) (*batchv1.Job, error) {
	image, err := r.getMustGatherImage(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errImageValidation, err)
	}

	// Inject the operator image URI from the pod's env variables
	operatorImage, varPresent := os.LookupEnv("OPERATOR_IMAGE")
	if !varPresent {
		err := goerror.New("operator image environment variable not found")
		reqLogger.Error(err, "Error: no operator image found for job template")
		return nil, err
	}

	directoryName := mustgatherutil.GenerateMustGatherDirectoryName(ctx, r.Client, time.Now())

	return getJobTemplate(image, operatorImage, *instance, r.TrustedCAConfigMap, directoryName), nil
}

func (r *MustGatherReconciler) getMustGatherImage(ctx context.Context, instance *mustgatherv1.MustGather) (string, error) {
	if instance.Spec.ImageStreamRef == nil {
		// Use default image
		return r.DefaultMustGatherImage, nil
	}

	// Use custom image from ImageStream
	imageStream := &imagev1.ImageStream{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: instance.Spec.ImageStreamRef.Name, Namespace: r.OperatorNamespace}, imageStream); err != nil {
		return "", fmt.Errorf("failed to get imagestream %s in namespace %s: %w", instance.Spec.ImageStreamRef.Name, r.OperatorNamespace, err)
	}

	var foundTag bool
	var pullable bool
	var image string
	for _, tag := range imageStream.Status.Tags {
		if tag.Tag == instance.Spec.ImageStreamRef.Tag {
			foundTag = true
			if len(tag.Items) > 0 && tag.Items[0].DockerImageReference != "" {
				pullable = true
				image = tag.Items[0].DockerImageReference
			}
			break
		}
	}

	if !foundTag {
		return "", fmt.Errorf("imagestream tag %s not found in imagestream %s", instance.Spec.ImageStreamRef.Tag, instance.Spec.ImageStreamRef.Name)
	}

	if !pullable {
		return "", fmt.Errorf("imagestream tag %s in imagestream %s is not pullable", instance.Spec.ImageStreamRef.Tag, instance.Spec.ImageStreamRef.Name)
	}

	return image, nil
}

// cleanupMustGatherResources cleans up the job and pods associated with a MustGather instance
func (r *MustGatherReconciler) cleanupMustGatherResources(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather) error {
	reqLogger.Info("cleaning up resources")

	// delete job from instance namespace
	tmpJob := &batchv1.Job{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      instance.Name,
	}, tmpJob)

	if err != nil {
		if !errors.IsNotFound(err) {
			reqLogger.Info("failed to get job", "job", instance.Name)
			return err
		}
		reqLogger.Info("job not found", "job", instance.Name)
		reqLogger.V(4).Info("successfully cleaned up mustgather resources")
		return nil
	}

	// Only delete Jobs owned by this MustGather instance to avoid deleting unrelated Jobs.
	ownedByInstance := false
	for _, ref := range tmpJob.GetOwnerReferences() {
		if ref.UID == instance.UID {
			ownedByInstance = true
			break
		}
	}
	if !ownedByInstance {
		reqLogger.Info("job exists but is not owned by this MustGather instance, skipping cleanup",
			"job", tmpJob.Name, "jobUID", tmpJob.UID, "instanceUID", instance.UID)
		return nil
	}

	// delete pods owned by job
	podList := &corev1.PodList{}
	if err = r.Client.List(ctx, podList,
		client.InNamespace(instance.Namespace),
		client.MatchingLabels{"controller-uid": string(tmpJob.UID)},
	); err != nil {
		reqLogger.Error(err, "failed to list pods", "Namespace", instance.Namespace, "UID", tmpJob.UID)
		return err
	}

	for i := range podList.Items {
		if err := r.Client.Delete(ctx, &podList.Items[i]); err != nil && !errors.IsNotFound(err) {
			reqLogger.Error(err, "failed to delete pods for job", "job", tmpJob.Name)
			return err
		}
	}
	reqLogger.Info("deleted pods for job", "job", tmpJob.Name)

	// finally delete job
	if err = r.Client.Delete(ctx, tmpJob); err != nil {
		reqLogger.Error(err, "failed to delete job", "job", tmpJob.Name)
		return err
	}
	reqLogger.Info("deleted job", "job", tmpJob.Name)

	if r.TrustedCAConfigMap != "" {
		if err := r.cleanupTrustedCAConfigMap(ctx, reqLogger, instance); err != nil {
			reqLogger.Error(err, "failed to cleanup trustedCA ConfigMap")
			return err
		}
	}

	reqLogger.V(4).Info("successfully cleaned up mustgather resources")
	return nil
}

// ensureTrustedCAConfigMap copies the trustedCA ConfigMap from the operator namespace to the CR namespace,
// adding the MustGather CR as an ownerReference.
func (r *MustGatherReconciler) ensureTrustedCAConfigMap(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather) error {
	if instance.Namespace == r.OperatorNamespace {
		reqLogger.V(4).Info("MustGather CR is in the same namespace as the operator, skipping ConfigMap copy")
		return nil
	}

	// fetch source config map
	sourceConfigMap := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: r.OperatorNamespace,
		Name:      r.TrustedCAConfigMap,
	}, sourceConfigMap)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("trustedCA ConfigMap not found in operator namespace",
				"configMapName", r.TrustedCAConfigMap, "operatorNamespace", r.OperatorNamespace)
		}

		return fmt.Errorf("failed to get trustedCA ConfigMap from operator namespace: %w", err)
	}

	existingConfigMap := &corev1.ConfigMap{}
	err = r.Client.Get(ctx, types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      r.TrustedCAConfigMap,
	}, existingConfigMap)

	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to check for existing ConfigMap in instance namespace: %w", err)
		}

		// config map doesn't exist, create it with ownerReference
		newConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.TrustedCAConfigMap,
				Namespace: instance.Namespace,
				Labels:    sourceConfigMap.Labels,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: instance.APIVersion,
						Kind:       instance.Kind,
						Name:       instance.Name,
						UID:        instance.UID,
					},
				},
			},
			Data: sourceConfigMap.Data,
		}

		if err = r.Client.Create(ctx, newConfigMap); err != nil {
			return fmt.Errorf("failed to create trustedCA ConfigMap in instance namespace: %w", err)
		}
		reqLogger.V(4).Info("successfully copied trustedCA ConfigMap",
			"configMapName", r.TrustedCAConfigMap)
		return nil
	}

	// ConfigMap exists, add this instance's ownerReference if missing
	ownerRefExists := false
	for _, ownerRef := range existingConfigMap.OwnerReferences {
		if ownerRef.UID == instance.UID {
			ownerRefExists = true
			break
		}
	}

	if !ownerRefExists {
		existingConfigMap.OwnerReferences = append(existingConfigMap.OwnerReferences, metav1.OwnerReference{
			APIVersion: instance.APIVersion,
			Kind:       instance.Kind,
			Name:       instance.Name,
			UID:        instance.UID,
		})

		if err = r.Client.Update(ctx, existingConfigMap); err != nil {
			return fmt.Errorf("failed to update ownerReferences on trustedCA ConfigMap: %w", err)
		}
		reqLogger.V(4).Info("added ownerReference to existing trustedCA ConfigMap",
			"configMapName", r.TrustedCAConfigMap)
	}

	return nil
}

// cleanupTrustedCAConfigMap removes the owner reference for the given instance from the trustedCA ConfigMap.
// If other owner references remain, the ConfigMap is UPDATEd to remove only this instance's owner reference;
// if the instance is the only owner, the ConfigMap is DELETEd.
func (r *MustGatherReconciler) cleanupTrustedCAConfigMap(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1.MustGather) error {
	if instance.Namespace == r.OperatorNamespace {
		return nil
	}

	existingConfigMap := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      r.TrustedCAConfigMap,
	}, existingConfigMap)

	if err != nil {
		// continue cleanup: in absence of the ConfigMap
		if errors.IsNotFound(err) {
			reqLogger.V(4).Info("trustedCA ConfigMap not found, nothing to cleanup",
				"configMapName", r.TrustedCAConfigMap)
			return nil
		}
		return fmt.Errorf("failed to get trustedCA ConfigMap: %w", err)
	}

	updatedOwnerRefs := make([]metav1.OwnerReference, 0, len(existingConfigMap.OwnerReferences))
	for _, ownerRef := range existingConfigMap.OwnerReferences {
		if ownerRef.UID != instance.UID {
			updatedOwnerRefs = append(updatedOwnerRefs, ownerRef)
		}
	}

	// If no owner references remain, delete the ConfigMap
	if len(updatedOwnerRefs) == 0 {
		if err = r.Client.Delete(ctx, existingConfigMap); err != nil {
			return fmt.Errorf("failed to delete trustedCA ConfigMap: %w", err)
		}

		reqLogger.V(4).Info("deleted trustedCA ConfigMap",
			"configMapName", r.TrustedCAConfigMap)
		return nil
	}

	// Else, update the ConfigMap to remove only this instance's owner reference
	updatedConfigMap := existingConfigMap.DeepCopy()
	updatedConfigMap.OwnerReferences = updatedOwnerRefs
	if err = r.Client.Update(ctx, updatedConfigMap); err != nil {
		return fmt.Errorf("failed to update trustedCA ConfigMap owner references: %w", err)
	}
	reqLogger.V(4).Info("removed ownerReference from trustedCA ConfigMap",
		"configMapName", r.TrustedCAConfigMap, "remainingNumOwners", len(updatedOwnerRefs))
	return nil
}
