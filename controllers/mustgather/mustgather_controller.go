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

	"github.com/go-logr/logr"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/openshift/must-gather-operator/pkg/localmetrics"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	// Fetch the MustGather instance
	instance := &mustgatherv1alpha1.MustGather{}
	err := r.GetClient().Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Check if the MustGather instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isMustGatherMarkedToBeDeleted := instance.GetDeletionTimestamp() != nil
	if isMustGatherMarkedToBeDeleted {
		reqLogger.Info("mustgather instance is marked for deletion")
		if contains(instance.GetFinalizers(), mustGatherFinalizer) {
			// Run finalization logic for mustGatherFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.

			// Clean up resources if RetainResourcesOnCompletion is false (default behavior)
			if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
				reqLogger.V(4).Info("running finalization logic for mustGatherFinalizer")
				err := r.cleanupMustGatherResources(ctx, reqLogger, instance)
				if err != nil {
					reqLogger.Error(err, "failed to cleanup MustGather resources during deletion")
					return reconcile.Result{}, err
				}
			}

			// Remove mustGatherFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			instance.SetFinalizers(remove(instance.GetFinalizers(), mustGatherFinalizer))
			err := r.GetClient().Update(ctx, instance)
			if err != nil {
				return r.ManageError(ctx, instance, err)
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(instance.GetFinalizers(), mustGatherFinalizer) {
		if err := r.addFinalizer(ctx, reqLogger, instance); err != nil {
			return reconcile.Result{}, err
		}
	}

	job, err := r.getJobFromInstance(ctx, instance)
	if err != nil {
		log.Error(err, "unable to get job from", "instance", instance)
		return r.ManageError(ctx, instance, err)
	}

	job1 := &batchv1.Job{}
	err = r.GetClient().Get(ctx, types.NamespacedName{
		Name:      job.GetName(),
		Namespace: job.GetNamespace(),
	}, job1)

	if err != nil {
		if !errors.IsNotFound(err) {
			// Error reading the object - requeue the request.
			log.Error(err, "unable to look up", "job", types.NamespacedName{
				Name:      job.GetName(),
				Namespace: job.GetNamespace(),
			})
			return r.ManageError(ctx, instance, err)
		}

		// Validate that the ServiceAccount exists before creating the Job.
		// This prevents the Job from being stuck in pending state due to a missing ServiceAccount.
		// If no ServiceAccount is specified, default to "default" which should exist in all namespaces.
		// Note: If the "default" SA has been deleted, this validation will catch it and report an error.
		saName := instance.Spec.ServiceAccountName
		if saName == "" {
			saName = "default"
			log.Info("no serviceAccountName specified, defaulting to 'default'", "namespace", instance.Namespace)
		}
		serviceAccount := &corev1.ServiceAccount{}
		err = r.GetClient().Get(ctx, types.NamespacedName{
			Namespace: instance.Namespace,
			Name:      saName,
		}, serviceAccount)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Error(err, "service account not found", "name", saName, "namespace", instance.Namespace)
				return r.setValidationFailureStatus(ctx, reqLogger, instance, ValidationServiceAccount, err)
			}

			log.Error(err, "failed to get service account (transient error, will retry)", "name", saName, "namespace", instance.Namespace)
			return reconcile.Result{Requeue: true}, err
		}

		// look up user secret
		if instance.Spec.UploadTarget != nil && instance.Spec.UploadTarget.SFTP != nil && instance.Spec.UploadTarget.SFTP.CaseManagementAccountSecretRef.Name != "" {
			secretName := instance.Spec.UploadTarget.SFTP.CaseManagementAccountSecretRef.Name
			userSecret := &corev1.Secret{}
			err = r.GetClient().Get(ctx, types.NamespacedName{
				Namespace: instance.Namespace,
				Name:      secretName,
			}, userSecret)
			if err != nil {
				if errors.IsNotFound(err) {
					log.Error(err, "secret not found", "name", secretName, "namespace", instance.Namespace)
					return r.ManageError(ctx, instance, fmt.Errorf("secret %q not found in namespace %q: please create the secret referenced by caseManagementAccountSecretRef: %w", secretName, instance.Namespace, err))
				}
				log.Error(err, "failed to get secret", "name", secretName, "namespace", instance.Namespace)
				return reconcile.Result{Requeue: true}, err
			}
		}

		// job is not there, create it.
		err = r.CreateResourceIfNotExists(ctx, instance, instance.Namespace, job)
		if err != nil {
			log.Error(err, "unable to create", "job", job)
			return r.ManageError(ctx, instance, err)
		}
		// Increment prometheus metrics for must gather total
		localmetrics.MetricMustGatherTotal.Inc()
		return r.ManageSuccess(ctx, instance)
	}

	// Check status of job and update any metric counts
	if job1.Status.Active > 0 {
		reqLogger.Info("mustgather Job pods are still running")
	} else {
		// if the job has been marked as Succeeded or Failed but instance has no DeletionTimestamp,
		// requeue instance to handle resource clean-up (delete secret, job, and MustGather)
		if job1.Status.Succeeded > 0 {
			reqLogger.Info("mustgather Job pods succeeded")
			// Update the MustGather CR status to indicate success
			instance.Status.Status = "Completed"
			instance.Status.Completed = true
			instance.Status.Reason = "MustGather Job pods succeeded"
			err := r.GetClient().Status().Update(ctx, instance)
			if err != nil {
				log.Error(err, "unable to update instance", "instance", instance)
				return r.ManageError(ctx, instance, err)
			}

			// Clean up resources if RetainResourcesOnCompletion is false (default behavior)
			if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
				err := r.cleanupMustGatherResources(ctx, reqLogger, instance)
				if err != nil {
					reqLogger.Error(err, "failed to cleanup MustGather resources")
					return r.ManageError(ctx, instance, err)
				}
			}
			return reconcile.Result{}, nil
		}
		backoffLimit := int32(0)
		if job1.Spec.BackoffLimit != nil {
			backoffLimit = *job1.Spec.BackoffLimit
		}
		if job1.Status.Failed > backoffLimit {
			reqLogger.Info("MustGather Job pods failed")
			// Increment prometheus metrics for must gather errors
			localmetrics.MetricMustGatherErrors.Inc()
			// Update the MustGather CR status to indicate failure
			instance.Status.Status = "Failed"
			instance.Status.Completed = true
			instance.Status.Reason = "MustGather Job pods failed"
			err := r.GetClient().Status().Update(ctx, instance)
			if err != nil {
				log.Error(err, "unable to update instance", "instance", instance)
				return r.ManageError(ctx, instance, err)
			}

			// Clean up resources if RetainResourcesOnCompletion is false (default behavior)
			if instance.Spec.RetainResourcesOnCompletion == nil || !*instance.Spec.RetainResourcesOnCompletion {
				err := r.cleanupMustGatherResources(ctx, reqLogger, instance)
				if err != nil {
					reqLogger.Error(err, "failed to cleanup MustGather resources")
					return r.ManageError(ctx, instance, err)
				}
			}
			return reconcile.Result{}, nil
		}
	}

	// if we get here it means that either
	// 1. the mustgather instance was updated, which we don't support and we are going to ignore
	// 2. the job was updated, probably the status piece. we should the update the status of the instance, not supported yet.

	return r.updateStatus(ctx, instance, job1)

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
	instance.Status.Status = "Failed"
	instance.Status.Completed = true
	instance.Status.Reason = fmt.Sprintf("%s validation failed: %v", validationType, validationErr)
	instance.Status.LastUpdate = metav1.Now()

	if statusErr := r.GetClient().Status().Update(ctx, instance); statusErr != nil {
		reqLogger.Error(statusErr, "failed to update status after validation error")
		return r.ManageError(ctx, instance, statusErr)
	}
	return reconcile.Result{}, nil
}

func (r *MustGatherReconciler) updateStatus(ctx context.Context, instance *mustgatherv1alpha1.MustGather, job *batchv1.Job) (reconcile.Result, error) {
	instance.Status.Completed = !job.Status.CompletionTime.IsZero()

	return r.ManageSuccess(ctx, instance)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MustGatherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mustgatherv1alpha1.MustGather{}, builder.WithPredicates(resourceGenerationOrFinalizerChangedPredicate())).
		Owns(&batchv1.Job{}, builder.WithPredicates(isStateUpdated())).
		Complete(r)
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
	// Inject the operator image URI from the pod's env variables
	operatorImage, varPresent := os.LookupEnv("OPERATOR_IMAGE")
	if !varPresent {
		err := goerror.New("operator image environment variable not found")
		log.Error(err, "Error: no operator image found for job template")
		return nil, err
	}

	return getJobTemplate(operatorImage, *instance), nil
}

// contains is a helper function for finalizer
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// remove is a helper function for finalizer
func remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
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
			reqLogger.Info(fmt.Sprintf("failed to get %s job", instance.Name))
			return err
		}
		reqLogger.Info(fmt.Sprintf("job %s not found", instance.Name))
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
		reqLogger.Error(err, fmt.Sprintf("failed to delete pods for job %s", tmpJob.Name))
		return err
	}
	reqLogger.Info(fmt.Sprintf("deleted pods for job %s", tmpJob.Name))

	// finally delete job
	err = r.GetClient().Delete(ctx, tmpJob)
	if err != nil {
		reqLogger.Error(err, fmt.Sprintf("failed to delete %s job", tmpJob.Name))
		return err
	}
	reqLogger.Info(fmt.Sprintf("deleted job %s", tmpJob.Name))

	reqLogger.V(4).Info("successfully cleaned up mustgather resources")
	return nil
}
