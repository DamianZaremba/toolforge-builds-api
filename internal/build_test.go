package internal

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	tektonFake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"gitlab.wikimedia.org/repos/toolforge/toolforge-builds-api/gen/models"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	k8sFake "k8s.io/client-go/kubernetes/fake"
	k8sTesting "k8s.io/client-go/testing"
	knative "knative.dev/pkg/apis/duck/v1beta1"
)

func TestGetPipelineRunsReturnsErrorIfApiReturnsError(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.AddReactor("list", "pipelineruns", func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
		return true, nil, fmt.Errorf("Dummy error!")
	})
	clients := Clients{
		Tekton: &mockTekton,
	}

	_, err := getPipelineRuns(&clients, "dummy-namespace", v1.ListOptions{})

	if err == nil {
		t.Fatalf("I was expecting an error, got: %s", err)
	}

}

func TestGetPipelineRunsReturnsSortedArrayOfPipelineRuns(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:              "one",
						Namespace:         "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Now().Add(-1 * time.Hour)},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:              "two",
						Namespace:         "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Now()},
					},
				},
			},
		},
	)
	clients := Clients{
		Tekton: mockTekton,
	}

	pipelineRuns, err := getPipelineRuns(&clients, "dummy-namespace", v1.ListOptions{})

	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}

	if len(pipelineRuns) != 2 {
		t.Fatalf("I was expecting 2 pipeline run, got: %d", len(pipelineRuns))
	}

	if pipelineRuns[0].Name != "two" {
		t.Fatalf("I was expecting the first pipeline run to be 'two', got: %s", pipelineRuns[0].Name)
	}

}

func TestDeletePipelineRunReturnsErrorIfApiReturnsError(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.AddReactor("delete", "pipelineruns", func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
		return true, nil, fmt.Errorf("Dummy error!")
	})
	clients := Clients{
		Tekton: &mockTekton,
	}

	err := deletePipelineRun(&clients, "dummy-namespace", "dummy-name")

	if err == nil {
		t.Fatalf("I was expecting an error, got: %s", err)
	}

}

func TestDeletePipelineRunDeletesTargetPipelineRun(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{ObjectMeta: v1.ObjectMeta{Name: "one", Namespace: "dummy-namespace"}},
				{ObjectMeta: v1.ObjectMeta{Name: "two", Namespace: "dummy-namespace"}},
			},
		},
	)
	clients := Clients{
		Tekton: mockTekton,
	}

	err := deletePipelineRun(&clients, "dummy-namespace", "one")

	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}

	pipelineRuns, err := clients.Tekton.TektonV1beta1().PipelineRuns("dummy-namespace").List(
		context.TODO(),
		v1.ListOptions{},
	)

	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}

	if len(pipelineRuns.Items) != 1 {
		t.Fatalf("I was expecting 1 pipeline run, got: %d", len(pipelineRuns.Items))
	}

}

func TestCleanupOldPipelineRuns(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-wrong-user", Namespace: "dummy-namespace", Labels: map[string]string{"user": "wrong-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 22, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-succeeded1", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-succeeded2", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 18, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-failed1", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "False"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-failed2", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-running", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status:     v1beta1.PipelineRunStatus{},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-timedout", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Type: "TimedOut"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipelinerun-cancelled", Namespace: "dummy-namespace", Labels: map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Type: "Cancelled"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
			},
		},
	)
	clients := Clients{
		Tekton: mockTekton,
	}
	buildConfig := map[string]int{"okBuildsToKeep": 1, "failedBuildsToKeep": 2}

	expectedPipelineRunNames := map[string]bool{
		"pipelinerun-wrong-user": false,
		"pipelinerun-succeeded2": true,
		"pipelinerun-failed1":    true,
		"pipelinerun-failed2":    true,
		"pipelinerun-running":    true,
	}

	cleanupOldPipelineRuns(&clients, "dummy-namespace", "test-user", buildConfig)

	pipelineRuns, err := clients.Tekton.TektonV1beta1().PipelineRuns("dummy-namespace").List(
		context.TODO(),
		v1.ListOptions{},
	)

	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}

	if len(pipelineRuns.Items) != 5 {
		t.Fatalf("I was expecting 5 pipeline run, got: %d", len(pipelineRuns.Items))
	}

	for _, pipelineRun := range pipelineRuns.Items {
		if _, found := expectedPipelineRunNames[pipelineRun.Name]; !found {
			t.Fatalf("I was not expecting pipeline run %s", pipelineRun.Name)
		}
	}

}

func TestLogsReturnsErrorIfNotAllowed(t *testing.T) {
	responder := Logs(
		"dummy-build-id",
		&Clients{},
		"dummy-namespace",
		"dummy-tool-name",
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 401 {
		t.Fatalf("I was expecting a 401 response, got: %s", recorder.Result().Status)
	}

}
func TestLogsReturnsNotFoundIfNoBuildsThere(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: make([]v1beta1.PipelineRun, 0),
		},
	)
	clients := Clients{
		Tekton: mockTekton,
	}

	responder := Logs(
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		&clients,
		"dummy-namespace",
		"dummy-tool",
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 404 {
		t.Fatalf("I was expecting a 404 response, got: %s", recorder.Result().Status)
	}

}

// This behavior happens when the cluster has no runs at all
func TestLogsReturnsNotFoundIfApiReturnsError(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.AddReactor("list", "pipelineruns", func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
		return true, nil, fmt.Errorf("Dummy error!")
	})
	clients := Clients{
		Tekton: &mockTekton,
	}

	responder := Logs(
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		&clients,
		"dummy-namespace",
		"dummy-tool",
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 404 {
		t.Fatalf("I was expecting a 404 response, got: %s", recorder.Result().Status)
	}

}

func TestLogsReturnsNotFoundIfApiReturnsMoreThanOneRun(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{ObjectMeta: v1.ObjectMeta{Name: "one"}},
				{ObjectMeta: v1.ObjectMeta{Name: "two"}},
			},
		},
	)
	clients := Clients{
		Tekton: mockTekton,
	}

	responder := Logs(
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		&clients,
		"dummy-namespace",
		"dummy-tool",
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 404 {
		t.Fatalf("I was expecting a 404 response, got: %s", recorder.Result().Status)
	}

}

func TestLogsReturnsEmptyLineIfRunHasNotStarted(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	pipelineRunList := v1beta1.PipelineRunList{
		Items: []v1beta1.PipelineRun{
			{
				Status: v1beta1.PipelineRunStatus{
					PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
						StartTime: nil,
					},
				},
			},
		},
	}
	mockTekton.Fake.AddReactor(
		"list",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, &pipelineRunList, nil
		},
	)
	clients := Clients{
		Tekton: &mockTekton,
	}

	responder := Logs(
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		&clients,
		"dummy-namespace",
		"dummy-tool",
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 200 {
		t.Fatalf("I was expecting a 200 response, got: %s", recorder.Result().Status)
	}

	var gottenLogs models.BuildLogs
	if err := gottenLogs.UnmarshalBinary(recorder.Body.Bytes()); err != nil {
		t.Fatalf("Got error unmarshalling response: \nerr:%s\nrawResponse:%s", err, recorder.Body.Bytes())
	}

	if len(gottenLogs.Lines) != 1 || gottenLogs.Lines[0] != "" {
		t.Fatalf("I was expecting only an empty line, got: %s", gottenLogs)
	}
}

func TestLogsReturnsAllLogsConcatenated(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	// avoid the object from being collected
	fakePipelineRunList := v1beta1.PipelineRunList{
		Items: []v1beta1.PipelineRun{
			{
				Status: v1beta1.PipelineRunStatus{
					PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
						StartTime: &v1.Time{Time: time.Now()},
						TaskRuns: map[string]*v1beta1.PipelineRunTaskRunStatus{
							"task-run-one": {
								Status: &v1beta1.TaskRunStatus{
									TaskRunStatusFields: v1beta1.TaskRunStatusFields{
										PodName: "dummy-pod",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	mockTekton.Fake.AddReactor(
		"list",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, &fakePipelineRunList, nil
		},
	)
	mockK8s := k8sFake.Clientset{}
	// The mocking of getLogs always returns one line "fake logs", it's hardcoded in the fake upstream
	// that's why we return `nil` as the return value does not matter
	mockK8s.AddReactor("get", "pods/logs", func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
		return true, nil, fmt.Errorf("no reaction implemented for verb:get resource:pods/log")
	})
	clients := Clients{
		Tekton: &mockTekton,
		K8s:    &mockK8s,
	}

	responder := Logs(
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		&clients,
		"dummy-namespace",
		"dummy-tool",
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 200 {
		t.Fatalf("I was expecting a 200 response, got: %s", recorder.Result().Status)
	}

	var gottenLogs models.BuildLogs
	if err := gottenLogs.UnmarshalBinary(recorder.Body.Bytes()); err != nil {
		t.Fatalf("Got error unmarshalling response: \nerr:%s\nrawResponse:%s", err, recorder.Body.Bytes())
	}

	// We get one line per getLogs fake call (hardcoded upstream)
	expectedLines := make([]string, 0)
	for _, container := range Containers {
		expectedLines = append(expectedLines, fmt.Sprintf("%s: fake logs", container))
	}

	if len(gottenLogs.Lines) != len(Containers) {
		t.Fatalf("I was expecting one line per container, got: %s", gottenLogs)
	}

	for index, expectedLine := range expectedLines {
		if expectedLine != gottenLogs.Lines[index] {
			t.Fatalf("I was expecting:\n%s\nBut got:\n%s", expectedLines, gottenLogs)
		}
	}
}

func TestStartReturnsInternalServerErrorOnException(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.Fake.AddReactor(
		"create",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, nil, fmt.Errorf("Dummy error")
		},
	)
	clients := Clients{
		Tekton: &mockTekton,
	}
	buildConfig := map[string]int{"okBuildsToKeep": 1, "failedBuildsToKeep": 2}

	responder := Start(
		"dummy-source-url",
		"dummy-ref",
		&clients,
		"dummy-namespace",
		"dummy-tool",
		"dummy-harbor-repository",
		"dummy-builder",
		buildConfig,
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 500 {
		t.Fatalf("I was expecting a 500 response, got: %s", recorder.Result().Status)
	}
}

func TestStartReturnsNewBuildName(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	expectedName := "new-pipelinerun"
	expectedRef := "dummy-ref"
	expectedSourceURL := "dummy-source-url"
	fakePipelineRun := v1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{Name: expectedName},
	}
	mockTekton.Fake.AddReactor(
		"create",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, &fakePipelineRun, nil
		},
	)
	clients := Clients{
		Tekton: &mockTekton,
	}
	buildConfig := map[string]int{"okBuildsToKeep": 1, "failedBuildsToKeep": 2}

	responder := Start(
		expectedSourceURL,
		expectedRef,
		&clients,
		"dummy-namespace",
		"dummy-tool",
		"dummy-harbor-repository",
		"dummy-builder",
		buildConfig,
	)

	recorder := httptest.NewRecorder()
	responder.WriteResponse(recorder, runtime.JSONProducer())

	if recorder.Result().StatusCode != 200 {
		t.Fatalf("I was expecting a 200 response, got: %s", recorder.Result().Status)
	}

	var gottenNewBuild models.NewBuild
	if err := gottenNewBuild.UnmarshalBinary(recorder.Body.Bytes()); err != nil {
		t.Fatalf("Got error unmarshalling response: \nerr:%s\nrawResponse:%s", err, recorder.Body.Bytes())
	}

	if gottenNewBuild.Name != expectedName {
		t.Fatalf("Got an unexpected name for the new build, got '%s', expected '%s'", gottenNewBuild.Name, expectedName)
	}

	if gottenNewBuild.Parameters.Ref != expectedRef {
		t.Fatalf("Got an unexpected ref for the new build, got '%s', expected '%s'", gottenNewBuild.Parameters.Ref, expectedRef)
	}

	if gottenNewBuild.Parameters.SourceURL != expectedSourceURL {
		t.Fatalf("Got an unexpected source url for the new build, got '%s', expected '%s'", gottenNewBuild.Parameters.SourceURL, expectedSourceURL)
	}
}
