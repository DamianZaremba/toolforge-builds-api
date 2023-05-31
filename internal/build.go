package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-openapi/runtime/middleware"
	log "github.com/sirupsen/logrus"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"gitlab.wikimedia.org/repos/toolforge/toolforge-builds-api/gen/models"
	"gitlab.wikimedia.org/repos/toolforge/toolforge-builds-api/gen/restapi/operations"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: Get the list of containers from the pod and sort by the task spec
var Containers = [...]string{
	"place-tools",
	"step-init",
	"place-scripts",
	"step-clone",
	"step-prepare",
	"step-copy-stack-toml",
	"step-detect",
	"step-analyze",
	"step-restore",
	"step-build",
	"step-export",
	"step-results",
}

func getContainerLogs(clients *Clients, container string, taskRun *v1beta1.PipelineRunTaskRunStatus, namespace string) (string, error) {
	logs := clients.K8s.CoreV1().Pods(BuildNamespace).GetLogs(
		taskRun.Status.PodName, &v1.PodLogOptions{
			Timestamps: true,
			Container:  container,
		},
	)
	result := logs.Do(context.TODO())
	err := result.Error()
	if err != nil {
		return "", err
	}
	rawLogs, err := result.Raw()
	if err != nil {
		return "", err
	}
	stringLogs := string(rawLogs)

	logLines := make([]string, strings.Count("\n", stringLogs))
	for _, logLine := range strings.Split(stringLogs, "\n") {
		logLines = append(logLines, fmt.Sprintf("%s: %s", container, logLine))
	}

	return strings.Join(logLines, "\n"), nil

}

func getPipelineRunLogs(pipelineRun *v1beta1.PipelineRun, clients *Clients, namespace string) (string, error) {
	// TODO: retrieve also logs from pods that failed to start

	if !pipelineRun.HasStarted() {
		return "", nil
	}

	for _, taskRun := range pipelineRun.Status.TaskRuns {
		var allLogs = make([]string, 0, len(Containers))
		for _, container := range Containers {
			newLogs, err := getContainerLogs(clients, container, taskRun, namespace)
			if err != nil {
				return "", err
			}
			allLogs = append(allLogs, newLogs)
		}
		return strings.Join(allLogs, "\n"), nil
	}

	// TODO: check if we should instead show something more
	return "", nil
}

func Logs(buildId string, clients *Clients, namespace string, toolName string) middleware.Responder {
	if err := ToolIsAllowedForBuild(toolName, buildId); err != nil {
		return operations.NewLogsUnauthorized().WithPayload(
			&models.Unauthorized{Message: fmt.Sprintf("%s", err)},
		)
	}

	pipelineRuns, err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).List(
		context.TODO(),
		metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", buildId)},
	)
	if err != nil {
		log.Warnf(
			"Got error when listing pipelineruns on namespace %s, maybe new cluster with no runs yet?: %s", namespace, err,
		)
		return operations.NewLogsNotFound().WithPayload(
			&models.NotFound{Message: "Unable to find any pipelineruns! New installation?"},
		)
	}

	log.Debugf("Get: Got %d pipeline runs!: %v", len(pipelineRuns.Items), pipelineRuns.Items)
	if len(pipelineRuns.Items) == 0 {
		return operations.NewLogsNotFound().WithPayload(
			&models.NotFound{Message: fmt.Sprintf("Unable to find build with id '%s'", buildId)},
		)
	} else if len(pipelineRuns.Items) > 1 {
		message := fmt.Sprintf("Got %d builds matching name %s, only 1 was expected.", len(pipelineRuns.Items), buildId)
		log.Warning(message)
		return operations.NewLogsNotFound().WithPayload(&models.NotFound{Message: message})
	}

	pipelineRun := pipelineRuns.Items[0]
	logs, err := getPipelineRunLogs(&pipelineRun, clients, namespace)
	if err != nil {
		message := fmt.Sprintf("Error getting the logs for %s: %s", buildId, err)
		log.Errorf(message)
		return operations.NewLogsInternalServerError().WithPayload(
			&models.InternalError{Message: message},
		)
	}

	return operations.NewLogsOK().WithPayload(
		&models.BuildLogs{
			Lines: strings.Split(logs, "\n"),
		},
	)
}

func Start(
	sourceURL string,
	ref string,
	clients *Clients,
	namespace string,
	toolName string,
	harborRepository string,
	builder string,
) middleware.Responder {
	// TODO: Check quotas
	// TODO: Create destination project in harbor if it does not exist
	log.Debugf("Starting a new build: ref=%s, toolName=%s, harborRepository=%s, builder=%s", ref, toolName, harborRepository, builder)
	newRun := v1beta1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s%s", toolName, BuildIdPrefix),
			Namespace:    BuildNamespace,
			Labels: map[string]string{
				"user": toolName,
			},
		},
		Spec: v1beta1.PipelineRunSpec{
			PipelineRef:        &v1beta1.PipelineRef{Name: "buildpacks"},
			ServiceAccountName: "buildpacks-service-account",
			Params: []v1beta1.Param{
				{
					Name: "BUILDER_IMAGE",
					Value: v1beta1.ParamValue{
						StringVal: builder,
						Type:      v1beta1.ParamTypeString,
					},
				},
				{
					Name: "APP_IMAGE",
					Value: v1beta1.ParamValue{
						StringVal: fmt.Sprintf("%s/tool-%s/%s:latest", harborRepository, toolName, toolName),
						Type:      v1beta1.ParamTypeString,
					},
				},
				{
					Name:  "SOURCE_URL",
					Value: v1beta1.ParamValue{StringVal: sourceURL, Type: v1beta1.ParamTypeString},
				},
				{
					Name:  "SOURCE_REFERENCE",
					Value: v1beta1.ParamValue{StringVal: ref, Type: v1beta1.ParamTypeString},
				},
			},
			Workspaces: []v1beta1.WorkspaceBinding{
				{
					Name:     "source-ws",
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
				{
					Name:     "cache-ws",
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	pipelineRun, err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).Create(
		context.TODO(),
		&newRun,
		metav1.CreateOptions{},
	)
	if err != nil {
		log.Warnf(
			"Got error when creating a new pipelinerun on namespace %s: %s", namespace, err,
		)
		return operations.NewStartInternalServerError().WithPayload(
			&models.InternalError{Message: "Unable to create a new build!"},
		)
	}

	return operations.NewStartOK().WithPayload(
		&models.NewBuild{
			Name: pipelineRun.Name,
			Parameters: &models.NewBuildParameters{
				Ref:       ref,
				SourceURL: sourceURL,
			},
		},
	)
}
