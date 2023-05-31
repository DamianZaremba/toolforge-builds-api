package internal

import (
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
)

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

	responder := Start(
		"dummy-source-url",
		"dummy-ref",
		&clients,
		"dummy-namespace",
		"dummy-tool",
		"dummy-harbor-repository",
		"dummy-builder",
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

	responder := Start(
		expectedSourceURL,
		expectedRef,
		&clients,
		"dummy-namespace",
		"dummy-tool",
		"dummy-harbor-repository",
		"dummy-builder",
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
