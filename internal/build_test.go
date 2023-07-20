package internal

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	tektonFake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
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
	api := BuildsApi{
		Clients: Clients{
			Tekton: mockTekton,
		},
		Config: Config{
			OkToKeep:       1,
			FailedToKeep:   2,
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	expectedPipelineRunNames := map[string]bool{
		"pipelinerun-wrong-user": false,
		"pipelinerun-succeeded2": true,
		"pipelinerun-failed1":    true,
		"pipelinerun-failed2":    true,
		"pipelinerun-running":    true,
	}

	cleanupOldPipelineRuns(&api.Clients, "dummy-namespace", "test-user", api.Config.OkToKeep, api.Config.FailedToKeep)

	pipelineRuns, err := api.Clients.Tekton.TektonV1beta1().PipelineRuns("dummy-namespace").List(
		context.TODO(),
		v1.ListOptions{},
	)

	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}

	if len(pipelineRuns.Items) != len(expectedPipelineRunNames) {
		t.Fatalf(
			"I was expecting %d pipeline run, got: %d",
			len(expectedPipelineRunNames),
			len(pipelineRuns.Items),
		)
	}

	for _, pipelineRun := range pipelineRuns.Items {
		if _, found := expectedPipelineRunNames[pipelineRun.Name]; !found {
			t.Fatalf("I was not expecting pipeline run %s", pipelineRun.Name)
		}
	}

}

func TestLogsReturnsErrorIfNotAllowed(t *testing.T) {
	api := BuildsApi{}

	code, _, err := Logs(
		&api,
		"dummy-build-id",
		"dummy-tool-name",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 401 {
		t.Fatalf("I was expecting a 401 response, got: %d", code)
	}

}

func TestLogsReturnsNotFoundIfNoBuildsThere(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: make([]v1beta1.PipelineRun, 0),
		},
	)
	api := BuildsApi{
		Clients: Clients{
			Tekton: mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _, err := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 404 {
		t.Fatalf("I was expecting a 404 response, got: %d", code)
	}

}

// This behavior happens when the cluster has no runs at all
func TestLogsReturnsNotFoundIfApiReturnsError(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.AddReactor("list", "pipelineruns", func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
		return true, nil, fmt.Errorf("Dummy error!")
	})
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _, err := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 404 {
		t.Fatalf("I was expecting a 404 response, got: %d", code)
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
	api := BuildsApi{
		Clients: Clients{
			Tekton: mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _, err := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 404 {
		t.Fatalf("I was expecting a 404 response, got: %d", code)
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
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, response, err := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got: %d", code)
	}

	gottenLogs := response.(gen.BuildLogs)
	if len(*gottenLogs.Lines) != 1 || (*gottenLogs.Lines)[0] != "" {
		t.Fatalf("I was expecting only an empty line, got: %s", *gottenLogs.Lines)
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
										Steps: []v1beta1.StepState{
											{Name: "clone"},
											{Name: "prepare"},
											{Name: "copy-stack-toml"},
											{Name: "detect"},
											{Name: "inject-buildpacks"},
											{Name: "analyze"},
											{Name: "restore"},
											{Name: "build"},
											{Name: "fix-permissions"},
											{Name: "export"},
											{Name: "results"},
										},
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
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
			K8s:    &mockK8s,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, response, err := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got: %d", code)
	}

	// We get one line per getLogs fake call (hardcoded upstream)
	expectedLines := make([]string, 0)
	containers := fakePipelineRunList.Items[0].Status.TaskRuns["task-run-one"].Status.Steps
	for _, container := range containers {
		expectedLines = append(expectedLines, fmt.Sprintf("%s: fake logs", container.Name))
	}

	gottenLogs := response.(gen.BuildLogs)
	if len(*gottenLogs.Lines) != len(containers) {
		t.Fatalf("I was expecting one line per container, got: %s", *gottenLogs.Lines)
	}

	for index, expectedLine := range expectedLines {
		if expectedLine != (*gottenLogs.Lines)[index] {
			t.Fatalf("I was expecting:\n%s\nBut got:\n%s", expectedLines, *gottenLogs.Lines)
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
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
		},
		Config: Config{
			HarborRepository: "dummy-harbor-repository",
			Builder:          "dummy-builder",
			OkToKeep:         1,
			FailedToKeep:     2,
			BuildIdPrefix:    BuildIdPrefix,
			BuildNamespace:   BuildNamespace,
		},
	}

	code, _, err := Start(
		&api,
		"dummy-source-url",
		"dummy-ref",
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}
	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
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
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
		},
		Config: Config{
			HarborRepository: "dummy-harbor-repository",
			Builder:          "dummy-builder",
			OkToKeep:         1,
			FailedToKeep:     2,
			BuildIdPrefix:    BuildIdPrefix,
			BuildNamespace:   BuildNamespace,
		},
	}

	code, response, err := Start(
		&api,
		expectedSourceURL,
		expectedRef,
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}
	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got: %d", code)
	}

	gottenNewBuild := response.(gen.NewBuild)
	if *gottenNewBuild.Name != expectedName {
		t.Fatalf("Got an unexpected name for the new build, got '%s', expected '%s'", *gottenNewBuild.Name, expectedName)
	}

	if *gottenNewBuild.Parameters.Ref != expectedRef {
		t.Fatalf("Got an unexpected ref for the new build, got '%s', expected '%s'", *gottenNewBuild.Parameters.Ref, expectedRef)
	}

	if *gottenNewBuild.Parameters.SourceUrl != expectedSourceURL {
		t.Fatalf("Got an unexpected source url for the new build, got '%s', expected '%s'", *gottenNewBuild.Parameters.SourceUrl, expectedSourceURL)
	}
}

func TestDeleteReturnsErrorIfNotAllowed(t *testing.T) {
	api := BuildsApi{
		Clients: Clients{},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _, err := Delete(
		&api,
		"dummy-build-id",
		"dummy-tool-name",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 401 {
		t.Fatalf("I was expecting a 401 response, got: %d", code)
	}

}

func TestDeleteReturnsNotFoundIfNoBuildsThere(t *testing.T) {
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: make([]v1beta1.PipelineRun, 0),
		},
	)
	api := BuildsApi{
		Clients: Clients{
			Tekton: mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _, err := Delete(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 404 {
		t.Fatalf("I was expecting a 404 response, got: %d", code)
	}

}

func TestDeleteReturnsInternalServerErrorOnException(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.Fake.AddReactor(
		"delete",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, nil, fmt.Errorf("Dummy error")
		},
	)
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _, err := Delete(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}

	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
	}
}

func TestDeleteReturnsDeletedBuildName(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	expectedId := fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix)
	mockTekton.Fake.AddReactor(
		"delete",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, nil, nil
		},
	)
	api := BuildsApi{
		Clients: Clients{
			Tekton: &mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, response, err := Delete(
		&api,
		expectedId,
		"dummy-tool",
	)

	if err != nil {
		t.Fatalf("Got unexpected error: %s", err)
	}
	recorder := httptest.NewRecorder()

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got (%s): %v", recorder.Result().Status, recorder.Body.String())
	}

	gottenDeletedId := response.(gen.BuildId)
	if *gottenDeletedId.Id != expectedId {
		t.Fatalf("Got unexpected build id, expected '%s', got '%s'", expectedId, *gottenDeletedId.Id)
	}
}
