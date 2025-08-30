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
	"reflect"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/openshift/must-gather-operator/pkg/k8sutil"
	"github.com/openshift/must-gather-operator/pkg/localmetrics"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "mustgather-controller"

	defaultMustGatherNamespace = "must-gather-operator"
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

const mustGatherFinalizer = "finalizer.mustgathers.managed.openshift.io"

//+kubebuilder:rbac:groups=managed.openshift.io,resources=mustgathers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=managed.openshift.io,resources=mustgathers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managed.openshift.io,resources=mustgathers/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs;jobs/finalizers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;create
//+kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods;services;services/finalizers;endpoints;persistentvolumeclaims;events;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete

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
	err := r.GetClient().Get(context.TODO(), request.NamespacedName, instance)
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

	if !r.IsInitialized(instance) {
		err := r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance)
			return r.ManageError(context.TODO(), instance, err)
		}
		return reconcile.Result{}, nil
	}

	// get operator namespace to manage resources in
	operatorNs, err := k8sutil.GetOperatorNamespace()
	if err != nil {
		operatorNs = defaultMustGatherNamespace
		log.Info(fmt.Sprintf("using default operator namespace: %s", defaultMustGatherNamespace))
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
			if !instance.Spec.RetainResourcesOnCompletion {
				reqLogger.V(4).Info("running finalization logic for mustGatherFinalizer")
				err := r.cleanupMustGatherResources(reqLogger, instance, operatorNs)
				if err != nil {
					reqLogger.Error(err, "failed to cleanup MustGather resources during deletion")
					return reconcile.Result{}, err
				}
			}

			// Remove mustGatherFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			instance.SetFinalizers(remove(instance.GetFinalizers(), mustGatherFinalizer))
			err := r.GetClient().Update(context.TODO(), instance)
			if err != nil {
				return r.ManageError(context.TODO(), instance, err)
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(instance.GetFinalizers(), mustGatherFinalizer) {
		if err := r.addFinalizer(reqLogger, instance); err != nil {
			return reconcile.Result{}, err
		}
	}

	job, err := r.getJobFromInstance(instance)
	if err != nil {
		log.Error(err, "unable to get job from", "instance", instance)
		return r.ManageError(context.TODO(), instance, err)
	}

	job1 := &batchv1.Job{}
	err = r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name:      job.GetName(),
		Namespace: job.GetNamespace(),
	}, job1)

	if err != nil {
		if errors.IsNotFound(err) {
			// look up user secret and copy it to operator namespace
			secretName := instance.Spec.CaseManagementAccountSecretRef.Name
			userSecret := &corev1.Secret{}
			err = r.GetClient().Get(context.TODO(), types.NamespacedName{
				Namespace: instance.Namespace,
				Name:      secretName,
			}, userSecret)
			if err != nil {
				log.Info(fmt.Sprintf("Error getting secret (%s)!", instance.Spec.CaseManagementAccountSecretRef.Name))
				return reconcile.Result{}, err
			}

			// create secret in the operator namespace
			newSecret := &corev1.Secret{}
			err = r.GetClient().Get(context.TODO(), types.NamespacedName{
				Namespace: operatorNs,
				Name:      secretName,
			}, newSecret)
			if err != nil {
				if errors.IsNotFound(err) {
					newSecret.Name = secretName
					newSecret.Namespace = operatorNs
					newSecret.Data = userSecret.Data
					newSecret.Type = userSecret.Type
					err = r.GetClient().Create(context.TODO(), newSecret)
					if err != nil {
						log.Error(err, fmt.Sprintf("Error creating new secret %s", secretName))
						return reconcile.Result{}, err
					}
				} else {
					log.Error(err, fmt.Sprintf("Error getting new secret %s", secretName))
					return reconcile.Result{}, err
				}
			}
			log.Info(fmt.Sprintf("Secret %s already exists in the %s namespace", secretName, operatorNs))

			// job is not there, create it.
			err = r.CreateResourceIfNotExists(context.TODO(), instance, operatorNs, job)
			if err != nil {
				log.Error(err, "unable to create", "job", job)
				return r.ManageError(context.TODO(), instance, err)
			}
			// Increment prometheus metrics for must gather total
			localmetrics.MetricMustGatherTotal.Inc()
			return r.ManageSuccess(context.TODO(), instance)
		}
		// Error reading the object - requeue the request.
		log.Error(err, "unable to look up", "job", types.NamespacedName{
			Name:      job.GetName(),
			Namespace: job.GetNamespace(),
		})
		return r.ManageError(context.TODO(), instance, err)
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
			err := r.GetClient().Status().Update(context.TODO(), instance)
			if err != nil {
				log.Error(err, "unable to update instance", "instance", instance)
				return r.ManageError(context.TODO(), instance, err)
			}

			// Clean up resources if RetainResourcesOnCompletion is false (default behavior)
			if !instance.Spec.RetainResourcesOnCompletion {
				err := r.cleanupMustGatherResources(reqLogger, instance, operatorNs)
				if err != nil {
					reqLogger.Error(err, "failed to cleanup MustGather resources")
					return r.ManageError(context.TODO(), instance, err)
				}
			}
			return reconcile.Result{}, nil
		}
		if job1.Status.Failed > 0 {
			reqLogger.Info("mustgather Job pods failed")
			// Increment prometheus metrics for must gather errors
			localmetrics.MetricMustGatherErrors.Inc()
			// Update the MustGather CR status to indicate failure
			instance.Status.Status = "Failed"
			instance.Status.Completed = true
			instance.Status.Reason = "MustGather Job pods failed"
			err := r.GetClient().Status().Update(context.TODO(), instance)
			if err != nil {
				log.Error(err, "unable to update instance", "instance", instance)
				return r.ManageError(context.TODO(), instance, err)
			}

			// Clean up resources if RetainResourcesOnCompletion is false (default behavior)
			if !instance.Spec.RetainResourcesOnCompletion {
				err := r.cleanupMustGatherResources(reqLogger, instance, operatorNs)
				if err != nil {
					reqLogger.Error(err, "failed to cleanup MustGather resources")
					return r.ManageError(context.TODO(), instance, err)
				}
			}
			return reconcile.Result{}, nil
		}
	}

	// if we get here it means that either
	// 1. the mustgather instance was updated, which we don't support and we are going to ignore
	// 2. the job was updated, probably the status piece. we should the update the status of the instance, not supported yet.

	return r.updateStatus(instance, job1)

}

func (r *MustGatherReconciler) updateStatus(instance *mustgatherv1alpha1.MustGather, job *batchv1.Job) (reconcile.Result, error) {
	instance.Status.Completed = !job.Status.CompletionTime.IsZero()

	return r.ManageSuccess(context.TODO(), instance)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MustGatherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mustgatherv1alpha1.MustGather{}, builder.WithPredicates(resourceGenerationOrFinalizerChangedPredicate())).
		Owns(&batchv1.Job{}, builder.WithPredicates(isStateUpdated())).
		Complete(r)
}

func (r *MustGatherReconciler) IsInitialized(instance *mustgatherv1alpha1.MustGather) bool {
	initialized := true

	if instance.Spec.ServiceAccountRef.Name == "" {
		instance.Spec.ServiceAccountRef.Name = "default"
		initialized = false
	}
	if reflect.DeepEqual(instance.Spec.ProxyConfig, configv1.ProxySpec{}) {
		platformProxy := &configv1.Proxy{}
		err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: "cluster"}, platformProxy)
		if err != nil {
			log.Error(err, "unable to find cluster proxy configuration")
		} else {
			instance.Spec.ProxyConfig = mustgatherv1alpha1.ProxySpec{
				HTTPProxy:  platformProxy.Spec.HTTPProxy,
				HTTPSProxy: platformProxy.Spec.HTTPSProxy,
				NoProxy:    platformProxy.Spec.NoProxy,
			}
			initialized = false
		}
	}
	return initialized
}

// addFinalizer is a function that adds a finalizer for the MustGather CR
func (r *MustGatherReconciler) addFinalizer(reqLogger logr.Logger, m *mustgatherv1alpha1.MustGather) error {
	reqLogger.Info("Adding Finalizer for the MustGather")
	m.SetFinalizers(append(m.GetFinalizers(), mustGatherFinalizer))

	// Update CR
	err := r.GetClient().Update(context.TODO(), m)
	if err != nil {
		reqLogger.Error(err, "Failed to update MustGather with finalizer")
		return err
	}
	return nil
}

func (r *MustGatherReconciler) getJobFromInstance(instance *mustgatherv1alpha1.MustGather) (*batchv1.Job, error) {
	// Inject the operator image URI from the pod's env variables
	operatorImage, varPresent := os.LookupEnv("OPERATOR_IMAGE")
	if !varPresent {
		err := goerror.New("operator image environment variable not found")
		log.Error(err, "Error: no operator image found for job template")
		return nil, err
	}

	version, err := r.getClusterVersionForJobTemplate("version")
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster version for job template: %w", err)
	}

	return getJobTemplate(operatorImage, version, *instance), nil
}

func (r *MustGatherReconciler) getClusterVersionForJobTemplate(clusterVersionName string) (string, error) {
	clusterVersion := &configv1.ClusterVersion{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: clusterVersionName}, clusterVersion)
	if err != nil {
		return "", fmt.Errorf("unable to get clusterversion '%v': %w", clusterVersionName, err)
	}

	var version string
	for _, history := range clusterVersion.Status.History {
		if history.State == "Completed" {
			version = history.Version
			break
		}
	}
	if version == "" {
		return "", goerror.New("unable to determine cluster version from status history")
	}

	parsedVersion, _ := semver.New(version)
	return fmt.Sprintf("%v.%v", parsedVersion.Major, parsedVersion.Minor), nil
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
func (r *MustGatherReconciler) cleanupMustGatherResources(reqLogger logr.Logger, instance *mustgatherv1alpha1.MustGather, operatorNs string) error {
	reqLogger.Info("cleaning up MustGather resources")
	// delete secret in the operator namespace
	tmpSecretName := instance.Spec.CaseManagementAccountSecretRef.Name
	tmpSecret := &corev1.Secret{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Namespace: operatorNs,
		Name:      tmpSecretName,
	}, tmpSecret)

	if err != nil {
		if !errors.IsNotFound(err) {
			reqLogger.Error(err, fmt.Sprintf("failed to get %s secret", tmpSecretName))
		}
		// Secret not found, continue with cleanup
	} else {
		err = r.GetClient().Delete(context.TODO(), tmpSecret)
		if err != nil {
			reqLogger.Error(err, fmt.Sprintf("failed to delete %s secret", tmpSecretName))
			return err
		}
		reqLogger.Info(fmt.Sprintf("successfully deleted secret %s", tmpSecretName))
	}

	// delete job from operator namespace
	tmpJob := &batchv1.Job{}
	err = r.GetClient().Get(context.TODO(), types.NamespacedName{
		Namespace: operatorNs,
		Name:      instance.Name,
	}, tmpJob)
	if err != nil {
		if !errors.IsNotFound(err) {
			reqLogger.Error(err, fmt.Sprintf("failed to get %s job", instance.Name))
		}
		// Job not found, continue with cleanup
	} else {
		// delete pods owned by job
		podList := &corev1.PodList{}
		listOpts := []client.ListOption{
			client.InNamespace(operatorNs),
			client.MatchingLabels{"controller-uid": string(tmpJob.UID)},
		}
		if err = r.GetClient().List(context.TODO(), podList, listOpts...); err != nil {
			reqLogger.Error(err, "failed to list pods", "Namespace", operatorNs, "UID", tmpJob.UID)
		} else {
			for _, tmpPod := range podList.Items {
				err = r.GetClient().Delete(context.TODO(), &tmpPod)
				if err != nil {
					reqLogger.Error(err, fmt.Sprintf("failed to delete %s pod", tmpPod.Name))
					return err
				}
			}
			reqLogger.Info(fmt.Sprintf("deleted pods for job %s", tmpJob.Name))
			// finally delete job
			err = r.GetClient().Delete(context.TODO(), tmpJob)
			if err != nil {
				reqLogger.Error(err, fmt.Sprintf("failed to delete %s job", tmpJob.Name))
				return err
			}
			reqLogger.Info(fmt.Sprintf("deleted job %s", tmpJob.Name))
		}
	}

	reqLogger.V(4).Info("successfully cleaned up mustgather resources")
	return nil
}
