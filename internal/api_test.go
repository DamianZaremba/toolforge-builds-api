// Copyright (c) 2024 Wikimedia Foundation and contributors.
// All Rights Reserved.
//
// This file is part of Toolforge Builds-Api.
//
// Toolforge Builds-Api is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Toolforge Builds-Api is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with Toolforge Builds-Api. If not, see <http://www.gnu.org/licenses/>.
package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	tektonPipelineV1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonFake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicFake "k8s.io/client-go/dynamic/fake"
	knative "knative.dev/pkg/apis/duck/v1"
)

func TestSubmitScheduledBuildsToTekton_NoExistingTektonRuns(t *testing.T) {
	maxParallelBuilds := 4
	userName := "test-user"
	namespace := "test-namespace"

	scheduledRuns := []tektonPipelineV1.PipelineRun{
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-1",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-2",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-3",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
	}

	scheduledUnstructuredObj := make([]runtime.Object, len(scheduledRuns))
	for i, run := range scheduledRuns {
		spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&run)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		metadata, found, err := unstructured.NestedMap(spec, "metadata")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if !found {
			t.Fatalf("Expected metadata to be found")
		}
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": fmt.Sprintf("%s/%s", ScheduledBuildResource.Group, ScheduledBuildResource.Version),
				"kind":       ScheduledBuildKind,
				"metadata":   metadata,
				"spec":       spec,
			},
		}
		scheduledUnstructuredObj[i] = obj
	}

	mockK8sScheme := runtime.NewScheme()
	mockK8sScheme.AddKnownTypeWithName(ScheduledBuildType, &unstructured.Unstructured{})
	mockK8sScheme.AddKnownTypeWithName(ScheduledBuildListType, &unstructured.UnstructuredList{})
	mockK8sCustom := dynamicFake.NewSimpleDynamicClient(mockK8sScheme, scheduledUnstructuredObj...)
	mockTekton := tektonFake.NewSimpleClientset()

	clients := &Clients{
		Tekton:    mockTekton,
		K8sCustom: mockK8sCustom,
	}

	config := &Config{
		BuildNamespace:    namespace,
		MaxParallelBuilds: maxParallelBuilds,
	}

	err := submitScheduledBuildsToTekton(clients, config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	createdPipelineRuns, err := clients.Tekton.TektonV1().PipelineRuns(namespace).List(
		context.TODO(),
		v1.ListOptions{},
	)
	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}

	if len(createdPipelineRuns.Items) != 3 {
		t.Fatalf("Expected 3 tekton pipeline runs to be created, got %d", len(createdPipelineRuns.Items))
	}

	expectedNames := map[string]struct{}{"scheduled-run-1": {}, "scheduled-run-2": {}, "scheduled-run-3": {}}
	if len(createdPipelineRuns.Items) != len(expectedNames) {
		t.Fatalf("Expected %d tekton pipeline runs to be created, got %d", len(expectedNames), len(createdPipelineRuns.Items))
	}
	for _, run := range createdPipelineRuns.Items {
		if _, ok := expectedNames[run.Name]; !ok {
			t.Fatalf("Unexpected tekton pipeline run created: %s", run.Name)
		}
	}
}

func TestSubmitScheduledBuildsToTekton_WithExistingMixedRuns(t *testing.T) {
	maxParallelBuilds := 4
	userName := "test-user"
	namespace := "test-namespace"

	scheduledRuns := []tektonPipelineV1.PipelineRun{
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-1",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-2",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-3",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
	}

	scheduledUnstructuredObj := make([]runtime.Object, len(scheduledRuns))
	for i, run := range scheduledRuns {
		spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&run)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		metadata, found, err := unstructured.NestedMap(spec, "metadata")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if !found {
			t.Fatalf("Expected metadata to be found")
		}
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": fmt.Sprintf("%s/%s", ScheduledBuildResource.Group, ScheduledBuildResource.Version),
				"kind":       ScheduledBuildKind,
				"metadata":   metadata,
				"spec":       spec,
			},
		}
		scheduledUnstructuredObj[i] = obj
	}

	mockK8sScheme := runtime.NewScheme()
	mockK8sScheme.AddKnownTypeWithName(ScheduledBuildType, &unstructured.Unstructured{})
	mockK8sScheme.AddKnownTypeWithName(ScheduledBuildListType, &unstructured.UnstructuredList{})
	mockK8sCustom := dynamicFake.NewSimpleDynamicClient(mockK8sScheme, scheduledUnstructuredObj...)
	mockTekton := tektonFake.NewSimpleClientset(
		&tektonPipelineV1.PipelineRunList{
			Items: []tektonPipelineV1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-pending",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-running",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-successful",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: tektonPipelineV1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Now()}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-failed",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Failed", Status: "False"}}},
						PipelineRunStatusFields: tektonPipelineV1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Now()}},
					},
				},
			},
		},
	)

	clients := &Clients{
		Tekton:    mockTekton,
		K8sCustom: mockK8sCustom,
	}

	config := &Config{
		BuildNamespace:    namespace,
		MaxParallelBuilds: maxParallelBuilds,
	}

	err := submitScheduledBuildsToTekton(clients, config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	pipelineRuns, err := mockTekton.TektonV1().PipelineRuns(namespace).List(context.TODO(), v1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list tekton pipeline runs: %v", err)
	}

	expectedNames := map[string]struct{}{"scheduled-run-2": {}, "scheduled-run-3": {}, "existing-pending": {}, "existing-running": {}, "existing-successful": {}, "existing-failed": {}}
	if len(pipelineRuns.Items) != len(expectedNames) {
		t.Fatalf("Expected %d tekton pipeline runs, got %d", len(expectedNames), len(pipelineRuns.Items))
	}

	for _, run := range pipelineRuns.Items {
		if _, ok := expectedNames[run.Name]; !ok {
			t.Fatalf("Unexpected tekton pipeline run found: %s", run.Name)
		}
	}
}

func TestSubmitScheduledBuildsToTekton_MaxCapacityReached(t *testing.T) {
	maxParallelBuilds := 4
	userName := "test-user"
	namespace := "test-namespace"

	scheduledRuns := []tektonPipelineV1.PipelineRun{
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-1",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-2",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scheduled-run-3",
				Namespace: namespace,
				Labels:    map[string]string{"user": userName},
			},
			Spec: tektonPipelineV1.PipelineRunSpec{
				PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			},
		},
	}

	scheduledUnstructuredObj := make([]runtime.Object, len(scheduledRuns))
	for i, run := range scheduledRuns {
		spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&run)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		metadata, found, err := unstructured.NestedMap(spec, "metadata")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if !found {
			t.Fatalf("Expected metadata to be found")
		}
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": fmt.Sprintf("%s/%s", ScheduledBuildResource.Group, ScheduledBuildResource.Version),
				"kind":       ScheduledBuildKind,
				"metadata":   metadata,
				"spec":       spec,
			},
		}
		scheduledUnstructuredObj[i] = obj
	}

	mockK8sScheme := runtime.NewScheme()
	mockK8sScheme.AddKnownTypeWithName(ScheduledBuildType, &unstructured.Unstructured{})
	mockK8sScheme.AddKnownTypeWithName(ScheduledBuildListType, &unstructured.UnstructuredList{})
	mockK8sCustom := dynamicFake.NewSimpleDynamicClient(mockK8sScheme, scheduledUnstructuredObj...)

	mockTekton := tektonFake.NewSimpleClientset(
		&tektonPipelineV1.PipelineRunList{
			Items: []tektonPipelineV1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-pending-1",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-pending-2",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-running-1",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-running-2",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-successful",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: tektonPipelineV1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Now()}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "existing-failed",
						Namespace: namespace,
						Labels:    map[string]string{"user": userName},
					},
					Status: tektonPipelineV1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Failed", Status: "False"}}},
						PipelineRunStatusFields: tektonPipelineV1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Now()}},
					},
				},
			},
		},
	)

	clients := &Clients{
		Tekton:    mockTekton,
		K8sCustom: mockK8sCustom,
	}

	config := &Config{
		BuildNamespace:    namespace,
		MaxParallelBuilds: maxParallelBuilds,
	}

	err := submitScheduledBuildsToTekton(clients, config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	pipelineRuns, err := mockTekton.TektonV1().PipelineRuns(namespace).List(context.TODO(), v1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list tekton pipeline runs: %v", err)
	}

	expectedNames := map[string]struct{}{"existing-pending-1": {}, "existing-pending-2": {}, "existing-running-1": {}, "existing-running-2": {}, "existing-successful": {}, "existing-failed": {}}
	if len(pipelineRuns.Items) != len(expectedNames) {
		t.Fatalf("Expected %d tekton pipeline runs, got %d", len(expectedNames), len(pipelineRuns.Items))
	}

	for _, run := range pipelineRuns.Items {
		if _, ok := expectedNames[run.Name]; !ok {
			t.Fatalf("Unexpected tekton pipeline run found: %s", run.Name)
		}
	}
}
