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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var _ = Describe("NodeCordonController", func() {
	var (
		k8sClient client.Client
		scheme    *runtime.Scheme
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().WithScheme(scheme).Build()
	})

	Context("When reconciling a resource", func() {
		const resourceName = "test-node"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}

		node := &corev1.Node{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Node")
			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: corev1.NodeSpec{
					Unschedulable: false,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			}
			err := k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance Node")
			resource := &corev1.Node{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, resource)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &NodeCordonReconciler{
				Client: k8sClient,
				Scheme: scheme,
			}

			_, err := controllerReconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should detect when a node is cordoned", func() {
			By("Cordoning the node")
			Expect(k8sClient.Get(ctx, typeNamespacedName, node)).To(Succeed())
			node.Spec.Unschedulable = true
			Expect(k8sClient.Update(ctx, node)).To(Succeed())

			By("Reconciling the cordoned node")
			controllerReconciler := &NodeCordonReconciler{
				Client: k8sClient,
				Scheme: scheme,
			}

			_, err := controllerReconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the node is marked as unschedulable")
			Expect(k8sClient.Get(ctx, typeNamespacedName, node)).To(Succeed())
			Expect(node.Spec.Unschedulable).To(BeTrue())
		})

		It("should handle node with taints", func() {
			By("Adding taints to the node and cordoning it")
			Expect(k8sClient.Get(ctx, typeNamespacedName, node)).To(Succeed())
			node.Spec.Unschedulable = true
			node.Spec.Taints = []corev1.Taint{
				{
					Key:    "node.kubernetes.io/unschedulable",
					Effect: corev1.TaintEffectNoSchedule,
				},
				{
					Key:    "maintenance",
					Value:  "true",
					Effect: corev1.TaintEffectNoExecute,
				},
			}
			Expect(k8sClient.Update(ctx, node)).To(Succeed())

			By("Reconciling the cordoned node with taints")
			controllerReconciler := &NodeCordonReconciler{
				Client: k8sClient,
				Scheme: scheme,
			}

			_, err := controllerReconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the node has taints")
			Expect(k8sClient.Get(ctx, typeNamespacedName, node)).To(Succeed())
			Expect(node.Spec.Taints).To(HaveLen(2))
			Expect(node.Spec.Unschedulable).To(BeTrue())
		})

		It("should handle node not found gracefully", func() {
			By("Attempting to reconcile a non-existent node")
			controllerReconciler := &NodeCordonReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			nonExistentName := types.NamespacedName{
				Name: "non-existent-node",
			}

			_, err := controllerReconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: nonExistentName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When testing cordon predicate", func() {
		It("should filter update events correctly", func() {
			predicate := cordonPredicate()

			By("Testing update event where cordon status changes")
			oldNode := &corev1.Node{
				Spec: corev1.NodeSpec{Unschedulable: false},
			}
			newNode := &corev1.Node{
				Spec: corev1.NodeSpec{Unschedulable: true},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}
			Expect(predicate.Update(updateEvent)).To(BeTrue())

			By("Testing update event where cordon status doesn't change")
			newNodeUnchanged := &corev1.Node{
				Spec: corev1.NodeSpec{Unschedulable: false},
			}

			updateEventUnchanged := event.UpdateEvent{
				ObjectOld: oldNode,
				ObjectNew: newNodeUnchanged,
			}
			Expect(predicate.Update(updateEventUnchanged)).To(BeFalse())
		})

		It("should filter create events correctly", func() {
			predicate := cordonPredicate()

			By("Testing create event with cordoned node")
			cordonedNode := &corev1.Node{
				Spec: corev1.NodeSpec{Unschedulable: true},
			}

			createEvent := event.CreateEvent{
				Object: cordonedNode,
			}
			Expect(predicate.Create(createEvent)).To(BeTrue())

			By("Testing create event with schedulable node")
			schedulableNode := &corev1.Node{
				Spec: corev1.NodeSpec{Unschedulable: false},
			}

			createEventSchedulable := event.CreateEvent{
				Object: schedulableNode,
			}
			Expect(predicate.Create(createEventSchedulable)).To(BeFalse())
		})

		It("should reject delete events", func() {
			predicate := cordonPredicate()

			deleteEvent := event.DeleteEvent{
				Object: &corev1.Node{},
			}
			Expect(predicate.Delete(deleteEvent)).To(BeFalse())
		})
	})
}) 