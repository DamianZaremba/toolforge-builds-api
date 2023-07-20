package internal

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	gen "gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type BuildStatus string

const (
	BuildStateRunning   BuildStatus = "running"
	BuildStateError     BuildStatus = "error"
	BuildStateOk        BuildStatus = "ok"
	BuildStateCancelled BuildStatus = "cancelled"
	BuildStateTimedOut  BuildStatus = "timeout"
	BuildStateUnknown   BuildStatus = "unknown"
)

func getContainersFromPod(client *Clients, podName string, namespace string) ([]string, error) {
	pod, err := client.K8s.CoreV1().Pods(namespace).Get(
		context.TODO(),
		podName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, err
	}
	var containers []string
	for _, container := range pod.Spec.InitContainers {
		containers = append(containers, container.Name)
	}
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
	}
	return containers, nil
}

func getPipelineRuns(clients *Clients, namespace string, listoptions metav1.ListOptions) ([]v1beta1.PipelineRun, error) {

	pipelineRuns, err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).List(
		context.TODO(),
		listoptions,
	)
	if err != nil {
		return nil, err
	}
	sort.Slice(pipelineRuns.Items, func(i, j int) bool {
		return !pipelineRuns.Items[i].CreationTimestamp.Before(&pipelineRuns.Items[j].CreationTimestamp)
	})
	return pipelineRuns.Items, nil
}

func getBuildStatusFromPipelineRun(pipelineRun v1beta1.PipelineRun) BuildStatus {

	if pipelineRun.Status.CompletionTime == nil {
		return BuildStateRunning
	}

	if pipelineRun.Status.Conditions == nil {
		return BuildStateError
	}

	for _, condition := range pipelineRun.Status.Conditions {
		if condition.Type == "Succeeded" && condition.Status == "True" {
			return BuildStateOk
		}

		if condition.Type == "Succeeded" && condition.Status == "False" {
			return BuildStateError
		}

		if condition.Type == "Cancelled" {
			return BuildStateCancelled
		}
		// TODO: use v1beta1.PipelineRunTimedOut when tekton >= 0.48.0
		if condition.Type == "TimedOut" {
			return BuildStateTimedOut
		}

	}

	return BuildStateUnknown
}

func filterPipelineRunsByStatus(pipelineRuns []v1beta1.PipelineRun, filter BuildStatus) []v1beta1.PipelineRun {
	var filteredPipelineRuns []v1beta1.PipelineRun
	for _, pipelineRun := range pipelineRuns {
		if getBuildStatusFromPipelineRun(pipelineRun) == filter {
			filteredPipelineRuns = append(filteredPipelineRuns, pipelineRun)
		}
	}
	return filteredPipelineRuns
}

func cleanupOldPipelineRuns(clients *Clients, namespace string, toolName string, okToKeep int, failedToKeep int) []error {
	pipelineRuns, err := getPipelineRuns(clients, namespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		return []error{err}
	}
	log.Debugf("Found %d pipelineruns. Cleaning up old runs...", len(pipelineRuns))
	runningPipelineRuns := filterPipelineRunsByStatus(pipelineRuns, BuildStateRunning)
	successfulPipelineRuns := filterPipelineRunsByStatus(pipelineRuns, BuildStateOk)
	failedPipelineRuns := filterPipelineRunsByStatus(pipelineRuns, BuildStateError)
	pipelineRunsToKeep := map[string]v1beta1.PipelineRun{}
	for _, pipelineRun := range runningPipelineRuns {
		pipelineRunsToKeep[pipelineRun.Name] = pipelineRun
	}
	log.Debugf("cleanupOldPipelineRuns: okToKeep, failedToKeep: %d, %d", okToKeep, failedToKeep)
	for idx, pipelineRun := range successfulPipelineRuns {
		if idx >= okToKeep {
			break
		}
		pipelineRunsToKeep[pipelineRun.Name] = pipelineRun
	}
	for idx, pipelineRun := range failedPipelineRuns {
		if idx >= failedToKeep {
			break
		}
		pipelineRunsToKeep[pipelineRun.Name] = pipelineRun
	}
	var deleteErrors []error
	for _, pipelineRun := range pipelineRuns {
		if _, found := pipelineRunsToKeep[pipelineRun.Name]; !found {
			log.Debugf("Deleting old pipelinerun %s", pipelineRun.Name)
			err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).Delete(
				context.TODO(),
				pipelineRun.Name,
				metav1.DeleteOptions{},
			)
			if err != nil {
				log.Warnf("Got error when deleting pipelinerun %s: %s", pipelineRun.Name, err)
				deleteErrors = append(deleteErrors, err)
			}
		}
	}
	return deleteErrors
}

func getContainerLogs(clients *Clients, container string, podName string, namespace string) (string, error) {
	logs := clients.K8s.CoreV1().Pods(namespace).GetLogs(
		podName, &v1.PodLogOptions{
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
		if logLine != "" {
			logLine = fmt.Sprintf("%s: %s", container, logLine)
		}
		logLines = append(logLines, logLine)
	}

	return strings.Join(logLines, "\n"), nil

}

func getPipelineRunLogs(pipelineRun *v1beta1.PipelineRun, clients *Clients, namespace string) (string, error) {
	// TODO: retrieve also logs from pods that failed to start

	if !pipelineRun.HasStarted() {
		return "", nil
	}

	allLogs := make([]string, 0)
	for _, taskRun := range pipelineRun.Status.TaskRuns {
		containers, err := getContainersFromPod(
			clients,
			taskRun.Status.PodName,
			namespace,
		)
		if err != nil {
			return "", err
		}
		for _, container := range containers {
			newLogs, err := getContainerLogs(clients, container, taskRun.Status.PodName, namespace)
			if err != nil {
				// As we changed the containers in the runs, some runs don't have the containers we look for
				if strings.HasPrefix(fmt.Sprintf("%s", err), fmt.Sprintf("container %s is not valid for pod", container)) {
					continue
				}
				return "", err
			}
			allLogs = append(allLogs, newLogs)
		}
	}
	return strings.Join(allLogs, "\n"), nil
}

func Logs(api *BuildsApi, buildId string, toolName string) (int, interface{}, error) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}, nil
	}

	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", buildId)})
	if err != nil {
		log.Warnf(
			"Got error when listing pipelineruns on namespace %s, maybe new cluster with no runs yet?: %s", api.Config.BuildNamespace, err,
		)
		message := "Unable to find any pipelineruns! New installation?"
		return http.StatusNotFound, gen.NotFound{Message: &message}, nil
	}

	log.Debugf("Get: Got %d pipeline runs!: %v", len(pipelineRuns), pipelineRuns)
	if len(pipelineRuns) == 0 {
		message := fmt.Sprintf("Unable to find build with id '%s'", buildId)
		return http.StatusNotFound, gen.NotFound{Message: &message}, nil

	} else if len(pipelineRuns) > 1 {
		message := fmt.Sprintf("Got %d builds matching name %s, only 1 was expected.", len(pipelineRuns), buildId)
		log.Warning(message)
		return http.StatusNotFound, gen.NotFound{Message: &message}, nil
	}

	pipelineRun := pipelineRuns[0]
	logs, err := getPipelineRunLogs(&pipelineRun, &api.Clients, api.Config.BuildNamespace)
	if err != nil {
		message := fmt.Sprintf("Error getting the logs for %s: %s", buildId, err)
		log.Errorf(message)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}, nil
	}

	lines := strings.Split(logs, "\n")
	return http.StatusOK, gen.BuildLogs{
		Lines: &lines,
	}, nil
}

func Start(
	api *BuildsApi,
	sourceURL string,
	ref string,
	toolName string,
) (int, interface{}, error) {
	// TODO: Check quotas
	// TODO: Create destination project in harbor if it does not exist
	cleanup_err := cleanupOldPipelineRuns(&api.Clients, api.Config.BuildNamespace, toolName, api.Config.OkToKeep, api.Config.FailedToKeep)
	for _, err := range cleanup_err {
		log.Warnf("Got error when cleaning up old pipeline runs: %s", err)
	}
	log.Debugf("Starting a new build: ref=%s, toolName=%s, harborRepository=%s, builder=%s", ref, toolName, api.Config.HarborRepository, api.Config.Builder)
	newRun := v1beta1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s%s", toolName, api.Config.BuildIdPrefix),
			Namespace:    api.Config.BuildNamespace,
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
						StringVal: api.Config.Builder,
						Type:      v1beta1.ParamTypeString,
					},
				},
				{
					Name: "APP_IMAGE",
					Value: v1beta1.ParamValue{
						StringVal: fmt.Sprintf("%s/tool-%s/tool-%s:latest", api.Config.HarborRepository, toolName, toolName),
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
				{
					Name:     "aptbuildpack-ws",
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	pipelineRun, err := api.Clients.Tekton.TektonV1beta1().PipelineRuns(api.Config.BuildNamespace).Create(
		context.TODO(),
		&newRun,
		metav1.CreateOptions{},
	)
	if err != nil {
		message := fmt.Sprintf("Got error when creating a new pipelinerun on namespace %s: %s", api.Config.BuildNamespace, err)
		log.Warn(message)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}, nil
	}

	buildParams := gen.NewBuildParameters{
		Ref:       &ref,
		SourceUrl: &sourceURL,
	}
	return http.StatusOK, gen.NewBuild{
		Name:       &pipelineRun.Name,
		Parameters: &buildParams,
	}, nil
}

func Delete(
	api *BuildsApi,
	buildId string,
	toolName string,
) (int, interface{}, error) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}, nil
	}
	// TODO: Delete also the associated image on harbor
	log.Debugf("Deleting build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)
	err := api.Clients.Tekton.TektonV1beta1().PipelineRuns(api.Config.BuildNamespace).Delete(
		context.TODO(),
		buildId,
		metav1.DeleteOptions{},
	)
	if err != nil {
		// A bit flaky way of handling, maybe improve in the future
		if strings.HasSuffix(err.Error(), "not found") {
			message := fmt.Sprintf("Build with id %s not found", buildId)
			return http.StatusNotFound, gen.NotFound{Message: &message}, nil
		}

		log.Warnf(
			"Got error when deleting pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Unable to delete build!"
		return http.StatusInternalServerError, gen.InternalError{Message: &message}, nil
	}

	return http.StatusOK, gen.BuildId{Id: &buildId}, nil
}

func Healthcheck(api *BuildsApi) (int, gen.HealthResponse, error) {
	_, err := api.Clients.K8s.CoreV1().Pods(api.Config.BuildNamespace).List(
		context.TODO(),
		metav1.ListOptions{
			Limit: 1,
		},
	)
	if err != nil {
		message := fmt.Sprintf("Unable to contact the k8s API: %s", err)
		status := gen.HealthResponseStatus(gen.ERROR)
		return http.StatusInternalServerError, gen.HealthResponse{
			Message: &message,
			Status:  &status,
		}, nil
	}

	message := "All systems normal"
	status := gen.HealthResponseStatus(gen.OK)
	return http.StatusOK, gen.HealthResponse{
		Message: &message,
		Status:  &status,
	}, nil
}
