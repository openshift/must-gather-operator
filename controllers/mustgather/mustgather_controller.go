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
	"slices"

	"github.com/go-logr/logr"
	imagev1 "github.com/openshift/api/image/v1"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/openshift/must-gather-operator/pkg/localmetrics"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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

// MustGatherReconciler reconciles a MustGather object
type MustGatherReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
	// TrustedCAConfigMap is the name of the ConfigMap containing the trusted CA certificate bundle
	TrustedCAConfigMap string
	// OperatorNamespace is the namespace where the operator is running
	OperatorNamespace string
	// DefaultMustGatherImage is the default must-gather image
	DefaultMustGatherImage string
}

const mustGatherFinalizer = "finalizer.mustgathers.operator.openshift.io"

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
// ServiceAccount read access needed for pre-flight validation before Job creation

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MustGather object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *MustGatherReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling MustGather")

	instance := &mustgatherv1alpha1.MustGather{}
	err := r.GetClient().Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if instance.GetDeletionTimestamp() != nil {
		return r.reconcileDeletion(ctx, reqLogger, instance)
	}

	if !slices.Contains(instance.GetFinalizers(), mustGatherFinalizer) {
		return reconcile.Result{}, r.addFinalizer(ctx, reqLogger, instance)
	}

	if r.TrustedCAConfigMap != "" {
		if err := r.ensureTrustedCAConfigMap(ctx, reqLogger, instance); err != nil {
			log.Error(err, "failed to ensure trustedCA ConfigMap exists")
			return r.ManageError(ctx, instance, err)
		}
	}

	job, err := r.getJobFromInstance(ctx, instance)
	if err != nil {
		log.Error(err, "unable to get job from", "instance", instance)
		return r.ManageError(ctx, instance, err)
	}

	job1 := &batchv1.Job{}
	err = r.GetClient().Get(ctx, types.NamespacedName{Name: job.GetName(), Namespace: job.GetNamespace()}, job1)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "unable to look up", "job", types.NamespacedName{Name: job.GetName(), Namespace: job.GetNamespace()})
			return r.ManageError(ctx, instance, err)
		}
		return r.reconcileNewJob(ctx, reqLogger, instance, job)
	}

	return r.reconcileExistingJob(ctx, reqLogger, instance, job1)
}

func (r *MustGatherReconciler) reconcileDeletion(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather) (reconcile.Result, error) {
	reqLogger.Info("mustgather instance is marked for deletion")
	if !slices.Contains(instance.GetFinalizers(), mustGatherFinalizer) {
		return reconcile.Result{}, nil
	}
	if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
		reqLogger.V(4).Info("running finalization logic for mustGatherFinalizer")
		if err := r.cleanupMustGatherResources(ctx, reqLogger, instance); err != nil {
			reqLogger.Error(err, "failed to cleanup MustGather resources during deletion")
			return reconcile.Result{}, err
		}
	}
	instance.SetFinalizers(slices.DeleteFunc(instance.GetFinalizers(), func(s string) bool { return s == mustGatherFinalizer }))
	if err := r.GetClient().Update(ctx, instance); err != nil {
		return r.ManageError(ctx, instance, err)
	}
	return reconcile.Result{}, nil
}

func (r *MustGatherReconciler) reconcileNewJob(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather, job *batchv1.Job) (reconcile.Result, error) {
	if err := r.validateServiceAccount(ctx, reqLogger, instance); err != nil {
		var re reconcileError
		if goerror.As(err, &re) {
			return re.result, re.err
		}
		return reconcile.Result{}, err
	}
	if instance.Spec.UploadTarget != nil && instance.Spec.UploadTarget.SFTP != nil && instance.Spec.UploadTarget.SFTP.CaseManagementAccountSecretRef.Name != "" {
		if result, err := r.validateSFTPCredentials(ctx, reqLogger, instance); err != nil {
			return result, err
		}
	}
	if err := r.CreateResourceIfNotExists(ctx, instance, instance.Namespace, job); err != nil {
		log.Error(err, "unable to create", "job", job)
		return r.ManageError(ctx, instance, err)
	}
	localmetrics.MetricMustGatherTotal.Inc()
	return r.ManageSuccess(ctx, instance)
}

// reconcileError carries a reconcile.Result alongside an error for use in reconcileNewJob.
type reconcileError struct {
	result reconcile.Result
	err    error
}

func (e reconcileError) Error() string { return e.err.Error() }

func (r *MustGatherReconciler) validateServiceAccount(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather) error {
	saName := instance.Spec.ServiceAccountName
	if saName == "" {
		saName = "default"
		log.Info("no serviceAccountName specified, defaulting to 'default'", "namespace", instance.Namespace)
	}
	serviceAccount := &corev1.ServiceAccount{}
	err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: saName}, serviceAccount)
	if err == nil {
		return nil
	}
	if errors.IsNotFound(err) {
		log.Error(err, "service account not found", "name", saName, "namespace", instance.Namespace)
		result, rerr := r.setValidationFailureStatus(ctx, reqLogger, instance, ValidationServiceAccount, err)
		return reconcileError{result, rerr}
	}
	log.Error(err, "failed to get service account (transient error, will retry)", "name", saName, "namespace", instance.Namespace)
	return reconcileError{reconcile.Result{Requeue: true}, err}
}

func (r *MustGatherReconciler) validateSFTPCredentials(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather) (reconcile.Result, error) {
	secretName := instance.Spec.UploadTarget.SFTP.CaseManagementAccountSecretRef.Name
	userSecret := &corev1.Secret{}
	err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: secretName}, userSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "secret not found", "secret", secretName, "namespace", instance.Namespace)
			return r.ManageError(ctx, instance, fmt.Errorf("secret %s not found in namespace %s: Please create the secret referenced by caseManagementAccountSecretRef", secretName, instance.Namespace))
		}
		log.Error(err, "error getting secret", "secret", secretName)
		return reconcile.Result{Requeue: true}, err
	}
	username, usernameExists := userSecret.Data["username"]
	password, passwordExists := userSecret.Data["password"]
	if !usernameExists || len(username) == 0 {
		validationErr := fmt.Errorf("sftp credentials secret %q is missing required field 'username'", secretName)
		reqLogger.Error(validationErr, "sftp credential validation failed")
		return r.setValidationFailureStatus(ctx, reqLogger, instance, ValidationSFTPCredentials, validationErr)
	}
	if !passwordExists || len(password) == 0 {
		validationErr := fmt.Errorf("sftp credentials secret %q is missing required field 'password'", secretName)
		reqLogger.Error(validationErr, "sftp credential validation failed")
		return r.setValidationFailureStatus(ctx, reqLogger, instance, ValidationSFTPCredentials, validationErr)
	}
	reqLogger.Info("Validating SFTP credentials before creating must-gather job")
	if validationErr := validateSFTPWithRetry(ctx, reqLogger, string(username), string(password), instance.Spec.UploadTarget.SFTP.Host); validationErr != nil {
		reqLogger.Error(validationErr, "SFTP credential validation failed")
		return r.setValidationFailureStatus(ctx, reqLogger, instance, ProtocolSFTP, validationErr)
	}
	reqLogger.Info("SFTP credentials validated successfully")
	return reconcile.Result{}, nil
}

func (r *MustGatherReconciler) reconcileExistingJob(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather, job1 *batchv1.Job) (reconcile.Result, error) {
	if job1.Status.Active > 0 {
		reqLogger.Info("mustgather Job pods are still running")
		return r.updateStatus(ctx, instance, job1)
	}
	if job1.Status.Succeeded > 0 {
		reqLogger.Info("mustgather Job pods succeeded")
		return r.handleJobCompletion(ctx, reqLogger, instance, "Completed", "MustGather Job pods succeeded")
	}
	backoffLimit := int32(0)
	if job1.Spec.BackoffLimit != nil {
		backoffLimit = *job1.Spec.BackoffLimit
	}
	if job1.Status.Failed > backoffLimit {
		reqLogger.Info("MustGather Job pods failed")
		localmetrics.MetricMustGatherErrors.Inc()
		return r.handleJobCompletion(ctx, reqLogger, instance, "Failed", "MustGather Job pods failed")
	}
	return r.updateStatus(ctx, instance, job1)
}

func (r *MustGatherReconciler) handleJobCompletion(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather, status string, reason string) (reconcile.Result, error) {
	instance.Status.Status = status
	instance.Status.Completed = true
	instance.Status.Reason = reason
	err := r.GetClient().Status().Update(ctx, instance)
	if err != nil {
		reqLogger.Error(err, "unable to update instance", "instance", instance.Name)
		return r.ManageError(ctx, instance, err)
	}

	if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
		err := r.cleanupMustGatherResources(ctx, reqLogger, instance)
		if err != nil {
			reqLogger.Error(err, "failed to cleanup MustGather resources")
			return r.ManageError(ctx, instance, err)
		}
	}
	return reconcile.Result{}, nil
}

func (r *MustGatherReconciler) updateStatus(ctx context.Context, instance *mustgatherv1alpha1.MustGather, job *batchv1.Job) (reconcile.Result, error) {
	instance.Status.Completed = !job.Status.CompletionTime.IsZero()

	return r.ManageSuccess(ctx, instance)
}

// setValidationFailureStatus updates the MustGather status to indicate a validation failure.
// It sets the status to Failed, marks it as completed, updates the reason with the validation type, and sets the timestamp.
// validationType should describe what kind of validation failed (e.g., "SFTP", "Service Account", "Secret").
func (r *MustGatherReconciler) setValidationFailureStatus(
	ctx context.Context,
	reqLogger logr.Logger,
	instance *mustgatherv1alpha1.MustGather,
	validationType string,
	validationErr error,
) (reconcile.Result, error) {
	errorMessage := fmt.Sprintf("%s validation failed: %v", validationType, validationErr)

	instance.Status.Status = "Failed"
	instance.Status.Completed = true
	instance.Status.Reason = errorMessage
	instance.Status.LastUpdate = metav1.Now()

	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "ReconcileError",
		Status:             metav1.ConditionTrue,
		Reason:             "ValidationFailed",
		Message:            errorMessage,
		ObservedGeneration: instance.GetGeneration(),
	})

	// Record a warning event for the validation failure
	r.GetRecorder().Event(instance, "Warning", "ProcessingError", errorMessage)

	if statusErr := r.GetClient().Status().Update(ctx, instance); statusErr != nil {
		reqLogger.Error(statusErr, "failed to update status after validation error")
		return r.ManageError(ctx, instance, statusErr)
	}
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MustGatherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&mustgatherv1alpha1.MustGather{}, builder.WithPredicates(resourceGenerationOrFinalizerChangedPredicate())).
		Owns(&batchv1.Job{}, builder.WithPredicates(isStateUpdated()))

	if r.TrustedCAConfigMap != "" {
		b = b.Owns(&corev1.ConfigMap{}, builder.WithPredicates(isNameEquals(r.TrustedCAConfigMap)))
	}

	return b.Complete(r)
}

// addFinalizer is a function that adds a finalizer for the MustGather CR
func (r *MustGatherReconciler) addFinalizer(ctx context.Context, reqLogger logr.Logger, m *mustgatherv1alpha1.MustGather) error {
	reqLogger.Info("Adding Finalizer for the MustGather")
	m.SetFinalizers(append(m.GetFinalizers(), mustGatherFinalizer))

	// Update CR
	err := r.GetClient().Update(ctx, m)
	if err != nil {
		reqLogger.Error(err, "Failed to update MustGather with finalizer")
		return err
	}
	return nil
}

func (r *MustGatherReconciler) getJobFromInstance(ctx context.Context, instance *mustgatherv1alpha1.MustGather) (*batchv1.Job, error) {

	image, err := r.getMustGatherImage(ctx, instance)
	if err != nil {
		_, validationErr := r.setValidationFailureStatus(ctx, log, instance, ValidationImageStream, err)
		if validationErr != nil {
			return nil, fmt.Errorf("failed to set validation failure status: %w (caused by: %w)", validationErr, err)
		}
		return nil, err
	}

	// Inject the operator image URI from the pod's env variables
	operatorImage, varPresent := os.LookupEnv("OPERATOR_IMAGE")
	if !varPresent {
		err := goerror.New("operator image environment variable not found")
		log.Error(err, "Error: no operator image found for job template")
		return nil, err
	}

	return getJobTemplate(image, operatorImage, *instance, r.TrustedCAConfigMap), nil
}

func (r *MustGatherReconciler) getMustGatherImage(ctx context.Context, instance *mustgatherv1alpha1.MustGather) (string, error) {
	if instance.Spec.ImageStreamRef == nil {
		// Use default image
		return r.DefaultMustGatherImage, nil
	}

	// Use custom image from ImageStream
	imageStream := &imagev1.ImageStream{}
	if err := r.GetClient().Get(ctx, types.NamespacedName{Name: instance.Spec.ImageStreamRef.Name, Namespace: r.OperatorNamespace}, imageStream); err != nil {
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

// cleanupMustGatherResources cleans up the secret, job, and pods associated with a MustGather instance
func (r *MustGatherReconciler) cleanupMustGatherResources(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather) error {
	reqLogger.Info("cleaning up resources")
	var err error

	// delete job from instance namespace
	tmpJob := &batchv1.Job{}
	err = r.GetClient().Get(ctx, types.NamespacedName{
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

	// delete pods owned by job
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
		client.MatchingLabels{"controller-uid": string(tmpJob.UID)},
	}

	if err = r.GetClient().List(ctx, podList, listOpts...); err != nil {
		reqLogger.Error(err, "failed to list pods", "Namespace", instance.Namespace, "UID", tmpJob.UID)
		return err
	}

	podObjs := make([]client.Object, len(podList.Items))
	for i, tmpPod := range podList.Items {
		podObjs[i] = &tmpPod
	}
	err = r.DeleteResourcesIfExist(ctx, podObjs)
	if err != nil {
		reqLogger.Error(err, "failed to delete pods for job", "job", tmpJob.Name)
		return err
	}
	reqLogger.Info("deleted pods for job", "job", tmpJob.Name)

	// finally delete job
	err = r.GetClient().Delete(ctx, tmpJob)
	if err != nil {
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

// ensureTrustedCAConfigMap copies the trustedCA ConfigMap from operator namespace to the CR namespace,
// adds/updates the ownerReference to include the MustGather CR.
func (r *MustGatherReconciler) ensureTrustedCAConfigMap(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather) error {
	if instance.Namespace == r.OperatorNamespace {
		reqLogger.V(4).Info("MustGather CR is in the same namespace as the operator, skipping ConfigMap copy")
		return nil
	}

	// fetch source config map
	sourceConfigMap := &corev1.ConfigMap{}
	err := r.GetClient().Get(ctx, types.NamespacedName{
		Namespace: r.OperatorNamespace,
		Name:      r.TrustedCAConfigMap,
	}, sourceConfigMap)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.V(2).Info("trustedCA ConfigMap not found in operator namespace, skipping copy",
				"configMapName", r.TrustedCAConfigMap, "operatorNamespace", r.OperatorNamespace)
		}

		return fmt.Errorf("failed to get trustedCA ConfigMap from operator namespace: %w", err)
	}

	existingConfigMap := &corev1.ConfigMap{}
	err = r.GetClient().Get(ctx, types.NamespacedName{
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

		err = r.GetClient().Create(ctx, newConfigMap)
		if err != nil {
			return fmt.Errorf("failed to create trustedCA ConfigMap in instance namespace: %w", err)
		}
		reqLogger.V(4).Info("successfully copied trustedCA ConfigMap",
			"configMapName", r.TrustedCAConfigMap)
		return nil
	}

	// ConfigMap exists, check if ownerReference for this instance already exists
	ownerRefExists := false
	for _, ownerRef := range existingConfigMap.OwnerReferences {
		if ownerRef.UID == instance.UID {
			ownerRefExists = true
			break
		}
	}

	// add ownerReference and update config map
	if !ownerRefExists {
		existingConfigMap.OwnerReferences = append(existingConfigMap.OwnerReferences, metav1.OwnerReference{
			APIVersion: instance.APIVersion,
			Kind:       instance.Kind,
			Name:       instance.Name,
			UID:        instance.UID,
		})

		err = r.GetClient().Update(ctx, existingConfigMap)
		if err != nil {
			return fmt.Errorf("failed to update ownerReferences on trustedCA ConfigMap: %w", err)
		}
		reqLogger.V(4).Info("added ownerReference to existing trustedCA ConfigMap",
			"configMapName", r.TrustedCAConfigMap)
	}

	return nil
}

// cleanupTrustedCAConfigMap removes the owner reference for the given instance from the trustedCA ConfigMap.
// If there are other owner references, UPDATE the ConfigMap to remove only this instance's owner reference.
// If the instance is the only owner, the ConfigMap is DELETEd.
func (r *MustGatherReconciler) cleanupTrustedCAConfigMap(ctx context.Context, reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather) error {
	if instance.Namespace == r.OperatorNamespace {
		return nil
	}

	existingConfigMap := &corev1.ConfigMap{}
	err := r.GetClient().Get(ctx, types.NamespacedName{
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
		err = r.GetClient().Delete(ctx, existingConfigMap)
		if err != nil {
			return fmt.Errorf("failed to delete trustedCA ConfigMap: %w", err)
		}

		reqLogger.V(4).Info("deleted trustedCA ConfigMap",
			"configMapName", r.TrustedCAConfigMap)
		return nil
	}

	// Else, update the ConfigMap to remove only this instance's owner reference
	updatedConfigMap := existingConfigMap.DeepCopy()
	updatedConfigMap.OwnerReferences = updatedOwnerRefs
	err = r.GetClient().Update(ctx, updatedConfigMap)
	if err != nil {
		return fmt.Errorf("failed to update trustedCA ConfigMap owner references: %w", err)
	}
	reqLogger.V(4).Info("removed ownerReference from trustedCA ConfigMap",
		"configMapName", r.TrustedCAConfigMap, "remainingNumOwners", len(updatedOwnerRefs))
	return nil
}
