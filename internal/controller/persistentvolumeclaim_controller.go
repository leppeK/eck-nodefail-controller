/*
Copyright 2024 leppek.

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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PersistentVolumeClaimReconciler reconciles a PersistentVolumeClaim object
type PersistentVolumeClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=persistentvolume,verbs=get;list;watch;delete;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PersistentVolumeClaim object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *PersistentVolumeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	pvc := corev1.PersistentVolumeClaim{}

	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		return ctrl.Result{}, nil
	}

	if pvc.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	if pvc.Labels[EckNodeFailKey] != EckNodeFailDelete {
		return ctrl.Result{}, nil
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		return ctrl.Result{}, nil
	}

	if err := r.labelPersistentVolume(ctx, pvc); err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	if err := r.Delete(ctx, &pvc); err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

func (r *PersistentVolumeClaimReconciler) labelPersistentVolume(ctx context.Context, pvc corev1.PersistentVolumeClaim) error {
	pv := corev1.PersistentVolume{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "", Name: pvc.Spec.VolumeName}, &pv); err != nil {
		return nil
	}

	if pv.Spec.StorageClassName != pvClass {
		return fmt.Errorf("PersistentVolume not matching class: %v", pvClass)
	}
	log.Log.Info("Labeling pv for deletion", "PersistentVolume", types.NamespacedName{Name: pv.Name, Namespace: ""})

	if pv.Labels == nil {
		pv.Labels = make(map[string]string)
	}

	pv.Labels[EckNodeFailKey] = EckNodeFailDelete

	if err := r.Update(ctx, &pv); err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		WithEventFilter(predicate.And( // Filters pods based on matchLabels map
			predicate.NewPredicateFuncs(func(object client.Object) bool {
				ol := object.GetLabels()
				for k, v := range deleteLabels {
					if ol[k] != v {
						return false
					}
				}
				return true
			}),
		)).
		Complete(r)
}
