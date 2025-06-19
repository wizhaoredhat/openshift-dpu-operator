/*
Copyright 2024.

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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// NodeCordonReconciler reconciles Node objects to detect cordon operations
type NodeCordonReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// NewNodeCordonReconciler creates a new NodeCordonReconciler
func NewNodeCordonReconciler(client client.Client, scheme *runtime.Scheme) *NodeCordonReconciler {
	return &NodeCordonReconciler{
		Client: client,
		Scheme: scheme,
	}
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=nodes/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// detect when nodes are cordoned (marked as unschedulable)
func (r *NodeCordonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Node instance
	node := &corev1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		// Node was deleted, nothing to do
		logger.Info("Node not found, likely deleted", "node", req.Name)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the node is cordoned (unschedulable)
	if node.Spec.Unschedulable {
		logger.Info("Node is cordoned (unschedulable)", 
			"node", node.Name,
			"creationTimestamp", node.CreationTimestamp,
			"taints", len(node.Spec.Taints))
		
		// Log additional details about the node state
		r.logNodeDetails(logger, node)
		
		// Here you can add custom logic to handle cordoned nodes
		// For example:
		// - Send alerts
		// - Update metrics
		// - Trigger remediation actions
		// - Log to external systems
		
	} else {
		logger.V(1).Info("Node is schedulable", "node", node.Name)
	}

	return ctrl.Result{}, nil
}

// logNodeDetails logs additional information about the node when it's cordoned
func (r *NodeCordonReconciler) logNodeDetails(logger logr.Logger, node *corev1.Node) {
	// Log node conditions
	for _, condition := range node.Status.Conditions {
		if condition.Status == corev1.ConditionFalse && condition.Type == corev1.NodeReady {
			logger.Info("Node is not ready", 
				"node", node.Name,
				"condition", condition.Type,
				"status", condition.Status,
				"reason", condition.Reason,
				"message", condition.Message)
		}
	}

	// Log taints that might be related to cordoning
	for _, taint := range node.Spec.Taints {
		logger.Info("Node has taint", 
			"node", node.Name,
			"key", taint.Key,
			"value", taint.Value,
			"effect", taint.Effect)
	}

	// Log node capacity and allocatable resources
	logger.Info("Node resource status",
		"node", node.Name,
		"capacity-cpu", node.Status.Capacity.Cpu().String(),
		"capacity-memory", node.Status.Capacity.Memory().String(),
		"allocatable-cpu", node.Status.Allocatable.Cpu().String(),
		"allocatable-memory", node.Status.Allocatable.Memory().String())
}

// cordonPredicate filters events to only process nodes that change cordon status
func cordonPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, ok := e.ObjectOld.(*corev1.Node)
			if !ok {
				return false
			}
			newNode, ok := e.ObjectNew.(*corev1.Node)
			if !ok {
				return false
			}
			
			// Only process if the unschedulable status changed
			return oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable
		},
		CreateFunc: func(e event.CreateEvent) bool {
			node, ok := e.Object.(*corev1.Node)
			if !ok {
				return false
			}
			// Process newly created nodes that are already cordoned
			return node.Spec.Unschedulable
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Don't process delete events for this use case
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			// Process generic events if the node is cordoned
			node, ok := e.Object.(*corev1.Node)
			if !ok {
				return false
			}
			return node.Spec.Unschedulable
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeCordonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(cordonPredicate()).
		Complete(r)
} 