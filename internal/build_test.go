package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	tektonFake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	k8sFake "k8s.io/client-go/kubernetes/fake"
	k8sTesting "k8s.io/client-go/testing"
	knative "knative.dev/pkg/apis/duck/v1beta1"
)

func TestToolNameToHarborProjectNameReturnsErrorIfNameIsInvalidToolforgeToolName(t *testing.T) {
	invalidToolNames := []string{
		"_username",
		".example",
		"name$",
		"123_",
		"my username",
		"!invalid",
	}
	for _, toolName := range invalidToolNames {
		_, err := ToolNameToHarborProjectName(toolName)
		if err == nil {
			t.Fatalf("I was expecting an error, got: %s", err)
		}
	}
}

func TestToolNameToHarborProjectNameReturnsCorrectHarborProjectName(t *testing.T) {
	testToolNames := [][]string{
		{
			"my-user--name",
			"tool-my-user-char.sep-name",
		},
		{
			"hello---world",
			"tool-hello-char.sep-char.sep-world",
		},
		{
			"this-is--a---123----user",
			"tool-this-is-char.sep-a-char.sep-char.sep-123-char.sep-char.sep-char.sep-user",
		},
		{
			"wikidata-redirects-conflicts-reports",
			"tool-wikidata-redirects-conflicts-reports",
		},
		{
			"wdq_checker",
			"tool-wdq_checker",
		},
		{
			"mixed_-_special-_-chars",
			"tool-mixed_char.sep-char.sep_special-char.sep_char.sep-chars",
		},
	}

	for index, test := range testToolNames {
		toolName := test[0]
		expected := test[1]
		result, err := ToolNameToHarborProjectName(toolName)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		if result != expected {
			t.Fatalf(
				"Test %d failed, expected: %s, got: %s",
				index,
				expected,
				result,
			)
		}
	}
}

func TestCreateHarborProjectForToolReturnsErrorIfHarborApiReturnsUnexpectedError(t *testing.T) {
	testServer1 := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusUnauthorized)
		rw.Write([]byte(fmt.Sprintf(`{"errors":[{ "code": %d, "message": "dummy error" }]}`, http.StatusUnauthorized)))
	}))
	testServer2 := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusBadGateway)
	}))
	defer testServer1.Close()
	defer testServer2.Close()
	api := BuildsApi{
		Clients: Clients{
			Http: &http.Client{},
		},
		Config: Config{
			HarborRepository: testServer1.URL,
			HarborUsername:   "dummy-harbor-username",
			HarborPassword:   "dummy-harbor-password",
		},
	}
	err := CreateHarborProjectForTool(&api, "dummy-tool-name")
	if err == nil {
		t.Fatalf("I was expecting an error, got: %s", err)
	}
	api.Config.HarborRepository = testServer2.URL
	err = CreateHarborProjectForTool(&api, "dummy-tool-name")
	if err == nil {
		t.Fatalf("I was expecting an error, got: %s", err)
	}
}

func TestCreateHarborProjectForToolReturnsNilIfProjectWasCreatedOrAlreadyExists(t *testing.T) {
	testServer1 := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusCreated)
	}))
	testServer2 := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusConflict)
		rw.Write([]byte(fmt.Sprintf(`{"errors":[{ "code": %d, "message": "dummy error" }]}`, http.StatusConflict)))

	}))
	defer testServer1.Close()
	defer testServer2.Close()
	api := BuildsApi{
		Clients: Clients{
			Http: &http.Client{},
		},
		Config: Config{
			HarborRepository: testServer1.URL,
			HarborUsername:   "dummy-harbor-username",
			HarborPassword:   "dummy-harbor-password",
		},
	}
	err := CreateHarborProjectForTool(&api, "dummy-tool-name")
	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}
	api.Config.HarborRepository = testServer2.URL
	err = CreateHarborProjectForTool(&api, "dummy-tool-name")
	if err != nil {
		t.Fatalf("I was not expecting an error, got: %s", err)
	}
}

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
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-cancelled", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 23, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Cancelled", Status: "False"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-failed1", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 22, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Failed", Status: "False"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-failed2", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 22, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "CreateRunFailed", Status: "False"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-running1", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 21, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-running2", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 20, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-running3", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 19, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-succeeded1", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 18, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 18, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-succeeded2", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 17, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "Succeeded", Status: "True"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)}},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name: "pipelinerun-timedout", Namespace: "dummy-namespace",
						CreationTimestamp: v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
						Labels:            map[string]string{"user": "test-user"}},
					Status: v1beta1.PipelineRunStatus{
						Status:                  knative.Status{Conditions: knative.Conditions{{Reason: "PipelineRunTimeout", Status: "False"}}},
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
		"pipelinerun-succeeded1": true,
		"pipelinerun-failed1":    true,
		"pipelinerun-failed2":    true,
		"pipelinerun-running1":   true,
		"pipelinerun-running2":   true,
		"pipelinerun-running3":   true,
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

	code, _ := Logs(
		&api,
		"dummy-build-id",
		"dummy-tool-name",
	)

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

	code, _ := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

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

	code, _ := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

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

	code, _ := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

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

	code, response := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got: %d", code)
	}

	gottenLogs := response.(gen.BuildLogs)
	if len(*gottenLogs.Lines) != 1 || (*gottenLogs.Lines)[0] != "" {
		t.Fatalf("I was expecting only an empty line, got: %s", *gottenLogs.Lines)
	}
}

func TestLogsReturnsAllLogsConcatenated(t *testing.T) {
	podName := "dummy-pod"
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
										PodName: podName,
									},
								},
							},
						},
					},
				},
			},
		},
	}
	mockTekton := tektonFake.Clientset{}
	mockK8s := *k8sFake.NewSimpleClientset(
		k8sRuntime.Object(&corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      podName,
				Namespace: BuildNamespace,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "place-tools"},
					{Name: "step-init"},
					{Name: "place-scripts"},
					{Name: "step-clone"},
					{Name: "step-prepare"},
					{Name: "step-copy-stack-toml"},
					{Name: "step-detect"},
					{Name: "step-inject-buildpacks"},
					{Name: "step-analyze"},
					{Name: "step-restore"},
					{Name: "step-build"},
					{Name: "step-fix-permissions"},
					{Name: "step-export"},
					{Name: "step-results"},
				},
			},
		}),
	)

	mockTekton.Fake.AddReactor(
		"list",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, &fakePipelineRunList, nil
		},
	)
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

	code, response := Logs(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got: %d", code)
	}

	// We get one line per getLogs fake call (hardcoded upstream)
	expectedLines := make([]string, 0)
	containers, _ := getContainersFromPod(&api.Clients, podName, BuildNamespace)
	for _, container := range containers {
		expectedLines = append(expectedLines, fmt.Sprintf("%s: fake logs", container))
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
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer testServer.Close()
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
			Http:   &http.Client{},
		},
		Config: Config{
			HarborRepository: testServer.URL,
			HarborUsername:   "dummy-harbor-username",
			HarborPassword:   "dummy-harbor-password",
			Builder:          "dummy-builder",
			OkToKeep:         1,
			FailedToKeep:     2,
			BuildIdPrefix:    BuildIdPrefix,
			BuildNamespace:   BuildNamespace,
		},
	}

	code, _ := Start(
		&api,
		"dummy-source-url",
		"dummy-ref",
		"dummy-tool",
	)

	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
	}
}

func TestStartReturnsInternalServerErrorIfCreateHarborProjectForToolReturnsError(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusUnauthorized)
		rw.Write([]byte(fmt.Sprintf(`{"errors":[{ "code": %d, "message": "dummy error" }]}`, http.StatusUnauthorized)))
	}))
	defer testServer.Close()
	api := BuildsApi{
		Clients: Clients{
			Http: &http.Client{},
		},
		Config: Config{
			HarborRepository: testServer.URL,
			HarborUsername:   "dummy-harbor-username",
			HarborPassword:   "dummy-harbor-password",
		},
	}

	code, _ := Start(
		&api,
		"dummy-source-url",
		"dummy-ref",
		"dummy-tool",
	)

	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
	}
}

func TestStartReturnsNewBuildName(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer testServer.Close()
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
			Http:   &http.Client{},
		},
		Config: Config{
			HarborRepository: testServer.URL,
			HarborUsername:   "dummy-harbor-username",
			HarborPassword:   "dummy-harbor-password",
			Builder:          "dummy-builder",
			OkToKeep:         1,
			FailedToKeep:     2,
			BuildIdPrefix:    BuildIdPrefix,
			BuildNamespace:   BuildNamespace,
		},
	}

	code, response := Start(
		&api,
		expectedSourceURL,
		expectedRef,
		"dummy-tool",
	)

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

	code, _ := Delete(
		&api,
		"dummy-build-id",
		"dummy-tool-name",
	)

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

	code, _ := Delete(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

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

	code, _ := Delete(
		&api,
		fmt.Sprintf("dummy-tool%sbuild", BuildIdPrefix),
		"dummy-tool",
	)

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

	code, response := Delete(
		&api,
		expectedId,
		"dummy-tool",
	)

	recorder := httptest.NewRecorder()

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got (%s): %v", recorder.Result().Status, recorder.Body.String())
	}

	gottenDeletedId := response.(gen.BuildId)
	if *gottenDeletedId.Id != expectedId {
		t.Fatalf("Got unexpected build id, expected '%s', got '%s'", expectedId, *gottenDeletedId.Id)
	}
}

func TestListReturnsInternalServerErrorOnException(t *testing.T) {
	mockTekton := tektonFake.Clientset{}
	mockTekton.Fake.AddReactor(
		"list",
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

	code, _ := List(
		&api,
		"dummy-tool",
	)

	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
	}
}

func TestListReturnsBuilds(t *testing.T) {
	toolName := "dummy-tool"
	expectedBuildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{Name: expectedBuildId, Namespace: BuildNamespace, Labels: map[string]string{"user": toolName}},
					Spec: v1beta1.PipelineRunSpec{
						Params: []v1beta1.Param{
							{Name: "BUILDER_IMAGE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "toolsbeta-harbor.wmcloud.org/toolforge/heroku-builder-classic:22"}},
							{Name: "APP_IMAGE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "192.168.188.129/tool-minikube-user/tool-raymond:latest"}},
							{Name: "SOURCE_URL", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "https://github.com/david-caro/wm-lol"}},
							{Name: "SOURCE_REFERENCE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "value4"}},
						},
					},
					Status: v1beta1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "True", Message: "All Tasks Succeeded", Reason: "Succeeded"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
							CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
							StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
						},
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
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, response := List(
		&api,
		toolName,
	)

	recorder := httptest.NewRecorder()

	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got (%s): %v", recorder.Result().Status, recorder.Body.String())
	}

	gottenBuilds := response.([]gen.Build)
	if len(gottenBuilds) != 1 {
		t.Fatalf("Got unexpected number of builds, expected 1, got %d", len(gottenBuilds))
	}
	if *gottenBuilds[0].BuildId != expectedBuildId {
		t.Fatalf("Got unexpected build id, expected '%s', got '%s'", expectedBuildId, *gottenBuilds[0].BuildId)
	}
}

func TestGetPipelineRunParam(t *testing.T) {
	type param struct {
		name  string
		value string
	}

	data := [4]param{
		{
			name:  "BUILDER_IMAGE",
			value: "toolsbeta-harbor.wmcloud.org/toolforge/heroku-builder-classic:22",
		},
		{
			name:  "APP_IMAGE",
			value: "192.168.188.129/tool-minikube-user/tool-raymond:latest",
		},
		{
			name:  "SOURCE_URL",
			value: "https://github.com/david-caro/wm-lol",
		},
		{
			name:  "SOURCE_REFERENCE",
			value: "value4",
		},
	}

	pipelinerun := v1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{Name: "test", Namespace: "test", Labels: map[string]string{"user": "test"}},
		Spec: v1beta1.PipelineRunSpec{
			Params: []v1beta1.Param{
				{Name: data[0].name, Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: data[0].value}},
				{Name: data[1].name, Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: data[1].value}},
				{Name: data[2].name, Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: data[2].value}},
				{Name: data[3].name, Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: data[3].value}},
			},
		},
		Status: v1beta1.PipelineRunStatus{
			Status: knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "True", Message: "All Tasks Succeeded", Reason: "Succeeded"}}},
			PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
				CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
				StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
			},
		},
	}

	// valid params
	for _, element := range data {
		result := getpipelineRunParam(pipelinerun, element.name)
		if result != element.value {
			t.Fatalf("Unexpected getpipelineRunParam() result for param '%s'. Expected '%s', but got '%s'.", element.name, element.value, result)
		}
	}

	// invalid param
	expected := "unknown"
	result := getpipelineRunParam(pipelinerun, "non-existant")
	if result != expected {
		t.Fatalf("Unexpected getpipelineRunParam() result for non-existant param. Expected '%s', but got '%s'.", expected, result)
	}
}

func TestGetReturnsBuildsOk(t *testing.T) {
	toolName := "dummy-tool"
	buildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{Name: buildId, Namespace: BuildNamespace, Labels: map[string]string{"user": toolName}},
					Spec: v1beta1.PipelineRunSpec{
						Params: []v1beta1.Param{
							{Name: "BUILDER_IMAGE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "toolsbeta-harbor.wmcloud.org/toolforge/heroku-builder-classic:22"}},
							{Name: "APP_IMAGE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "192.168.188.129/tool-minikube-user/tool-raymond:latest"}},
							{Name: "SOURCE_URL", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "https://github.com/david-caro/wm-lol"}},
							{Name: "SOURCE_REFERENCE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "value4"}},
						},
					},
					Status: v1beta1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Type: "Succeeded", Status: "True", Message: "All Tasks Succeeded", Reason: "Succeeded"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
							CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
							StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
						},
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
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	recorder := httptest.NewRecorder()
	code, response := Get(
		&api,
		buildId,
		toolName,
	)

	expected_code := http.StatusOK
	if code != expected_code {
		t.Fatalf("I was expecting a '%d' response, got (%d): %v", expected_code, code, recorder.Body.String())
	}

	build := response.(*gen.Build)
	if *build.BuildId != buildId {
		t.Fatalf("Got unexpected build id, expected '%s', got '%s'", buildId, *build.BuildId)
	}
}

func TestGetReturnsBuildsNotAuth(t *testing.T) {
	toolName := "dummy-tool"
	buildId := "some-non-authorized-build-name"
	mockTekton := tektonFake.NewSimpleClientset()
	api := BuildsApi{
		Clients: Clients{
			Tekton: mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	recorder := httptest.NewRecorder()
	code, response := Get(
		&api,
		buildId,
		toolName,
	)

	expected_code := http.StatusUnauthorized
	if code != expected_code {
		t.Fatalf("I was expecting a '%d' response, got '%d'. %v", expected_code, code, recorder.Body.String())
	}

	expected_response := fmt.Sprintf("user %s not allowed to act on build %s", toolName, buildId)
	resp := response.(gen.Unauthorized)
	if *resp.Message != expected_response {
		t.Fatalf("Expected response '%s' but got '%s'", expected_response, *resp.Message)
	}
}

func TestGetReturnsBuildsNotFound(t *testing.T) {
	toolName := "dummy-tool"
	buildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)
	mockTekton := tektonFake.NewSimpleClientset()
	api := BuildsApi{
		Clients: Clients{
			Tekton: mockTekton,
		},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	recorder := httptest.NewRecorder()
	code, response := Get(
		&api,
		buildId,
		toolName,
	)

	expected_code := http.StatusNotFound
	if code != expected_code {
		t.Fatalf("I was expecting a '%d' response, got '%d'. %v", expected_code, code, recorder.Body.String())
	}

	expected_response := fmt.Sprintf("Build with id %s not found.", buildId)
	resp := response.(gen.NotFound)
	if *resp.Message != expected_response {
		t.Fatalf("Expected response '%s' but got '%s'", expected_response, *resp.Message)
	}
}

func TestGetReturnsAPIError(t *testing.T) {
	toolName := "dummy-tool"
	buildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)

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

	recorder := httptest.NewRecorder()
	code, response := Get(
		&api,
		buildId,
		toolName,
	)

	expected_code := http.StatusInternalServerError
	if code != expected_code {
		t.Fatalf("I was expecting a '%d' response, got '%d'. %v", expected_code, code, recorder.Body.String())
	}

	expected_response := "Unable to get build! This might be a bug. Please contact a Toolforge admin."
	resp := response.(gen.InternalError)
	if *resp.Message != expected_response {
		t.Fatalf("Expected response '%s' but got '%s'", expected_response, *resp.Message)
	}
}

func TestCancelReturnsErrorIfNotAllowed(t *testing.T) {
	api := BuildsApi{
		Clients: Clients{},
		Config: Config{
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, _ := Cancel(
		&api,
		"dummy-build-id",
		"dummy-tool-name",
	)

	if code != 401 {
		t.Fatalf("I was expecting a 401 response, got: %d", code)
	}
}

func TestCancelReturnsNotFoundIfNoBuildsThere(t *testing.T) {
	toolName := "dummy-tool"
	buildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)
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
	code, _ := Cancel(
		&api,
		buildId,
		toolName,
	)

	if code != 404 {
		t.Fatalf("I was expecting a 404 response, got: %d", code)
	}
}

func TestCancelReturnsInternalServerErrorOnException(t *testing.T) {
	toolName := "dummy-tool"
	buildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)
	mockTekton := tektonFake.Clientset{}
	fakePipelineRun := v1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{Name: buildId, Namespace: BuildNamespace, Labels: map[string]string{"user": toolName}},
	}

	mockTekton.Fake.AddReactor(
		"get",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, nil, fmt.Errorf("dummy error")
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
	code, _ := Cancel(&api, buildId, toolName)
	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
	}

	mockTekton.Fake.PrependReactor(
		"get",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, &fakePipelineRun, nil
		},
	)
	mockTekton.Fake.AddReactor(
		"update",
		"pipelineruns",
		func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
			return true, nil, fmt.Errorf("dummy error")
		},
	)
	api.Clients.Tekton = &mockTekton
	code, _ = Cancel(&api, buildId, toolName)
	if code != 500 {
		t.Fatalf("I was expecting a 500 response, got: %d", code)
	}
}

func TestCancelReturnsConflictIfBuildIsNotCancellable(t *testing.T) {
	toolName := "dummy-tool"
	mockTekton := tektonFake.Clientset{}
	uncancellablePipelineRuns := []v1beta1.PipelineRun{
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("%s%s-successful-build", toolName, BuildIdPrefix),
				Namespace: BuildNamespace, Labels: map[string]string{"user": toolName},
			},
			Status: v1beta1.PipelineRunStatus{
				Status: knative.Status{Conditions: knative.Conditions{{Status: "True"}}},
				PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
					CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
					StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
				},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("%s%s-failed-build", toolName, BuildIdPrefix),
				Namespace: BuildNamespace, Labels: map[string]string{"user": toolName},
			},
			Status: v1beta1.PipelineRunStatus{
				Status: knative.Status{Conditions: knative.Conditions{{Status: "False"}}},
				PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
					CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
					StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
				},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("%s%s-timedout-build", toolName, BuildIdPrefix),
				Namespace: BuildNamespace, Labels: map[string]string{"user": toolName},
			},
			Status: v1beta1.PipelineRunStatus{
				Status: knative.Status{Conditions: knative.Conditions{{Status: "False", Reason: "PipelineRunTimeout"}}},
				PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
					CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
					StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
				},
			},
		},
		{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("%s%s-cancelled-build", toolName, BuildIdPrefix),
				Namespace: BuildNamespace, Labels: map[string]string{"user": toolName},
			},
			Status: v1beta1.PipelineRunStatus{
				Status: knative.Status{Conditions: knative.Conditions{{Status: "False", Reason: "PipelineRunCancelled"}}},
				PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
					CompletionTime: &v1.Time{Time: time.Date(2023, 6, 8, 16, 0, 0, 0, time.UTC)},
					StartTime:      &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
				},
			},
		},
	}

	for _, pipelineRun := range uncancellablePipelineRuns {
		mockTekton.Fake.PrependReactor(
			"get",
			"pipelineruns",
			func(action k8sTesting.Action) (handled bool, ret k8sRuntime.Object, err error) {
				return true, &pipelineRun, nil
			},
		)
		api := BuildsApi{
			Clients: Clients{
				Tekton: &mockTekton,
			},
			Config: Config{
				OkToKeep:       1,
				FailedToKeep:   2,
				BuildIdPrefix:  BuildIdPrefix,
				BuildNamespace: BuildNamespace,
			},
		}
		code, _ := Cancel(&api, pipelineRun.Name, toolName)
		if code != 409 {
			t.Fatalf("I was expecting a 409 response, got: %d", code)
		}
	}
}

func TestCancelReturnsCancelledBuild(t *testing.T) {
	toolName := "dummy-tool"
	buildId := fmt.Sprintf("%s%sbuild", toolName, BuildIdPrefix)
	mockTekton := tektonFake.NewSimpleClientset(
		&v1beta1.PipelineRunList{
			Items: []v1beta1.PipelineRun{
				{
					ObjectMeta: v1.ObjectMeta{Name: buildId, Namespace: BuildNamespace, Labels: map[string]string{"user": toolName}},
					Spec: v1beta1.PipelineRunSpec{
						Params: []v1beta1.Param{
							{Name: "BUILDER_IMAGE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "toolsbeta-harbor.wmcloud.org/toolforge/heroku-builder-classic:22"}},
							{Name: "APP_IMAGE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "192.168.188.129/tool-minikube-user/tool-raymond:latest"}},
							{Name: "SOURCE_URL", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "https://github.com/david-caro/wm-lol"}},
							{Name: "SOURCE_REFERENCE", Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "value4"}},
						},
					},
					Status: v1beta1.PipelineRunStatus{
						Status: knative.Status{Conditions: knative.Conditions{{Reason: "Running", Status: "Unknown"}}},
						PipelineRunStatusFields: v1beta1.PipelineRunStatusFields{
							StartTime: &v1.Time{Time: time.Date(2023, 6, 8, 15, 0, 0, 0, time.UTC)},
						},
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
			BuildIdPrefix:  BuildIdPrefix,
			BuildNamespace: BuildNamespace,
		},
	}

	code, response := Cancel(&api, buildId, toolName)
	if code != 200 {
		t.Fatalf("I was expecting a 200 response, got: %d", code)
	}

	gottenBuildId := response.(gen.BuildId)
	if *gottenBuildId.Id != buildId {
		t.Fatalf("Got unexpected build id, expected '%s', got '%s'", buildId, *gottenBuildId.Id)
	}
}
