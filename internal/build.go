package internal

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	gen "gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
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
	"step-inject-buildpacks",
	"step-analyze",
	"step-restore",
	"step-build",
	"step-fix-permissions",
	"step-export",
	"step-results",
}

func getPipelineRuns(clients *Clients, namespace string, listoptions metav1.ListOptions) ([]v1beta1.PipelineRun, error) {

	pipelineRuns, err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).List(
		context.TODO(),
		listoptions,
	)
	if err != nil {
		log.Warnf(
			"Got error when listing pipelineruns on namespace %s (%v), maybe new cluster with no runs yet?: %s", namespace, listoptions, err,
		)
		return nil, err
	}
	sort.Slice(pipelineRuns.Items, func(i, j int) bool {
		return !pipelineRuns.Items[i].CreationTimestamp.Before(&pipelineRuns.Items[j].CreationTimestamp)
	})
	return pipelineRuns.Items, nil
}

func getBuildConditionFromPipelineRun(run *v1beta1.PipelineRun) gen.BuildCondition {
	buildCondition := &gen.BuildCondition{}
	message := ""
	var status gen.BuildStatus

	if run.Status.Conditions == nil {
		status = gen.BUILDUNKNOWN
		message = fmt.Sprintf("build status is unknown. Check the logs with `toolforge build logs %s`", run.Name)
		buildCondition.Status = &status
		buildCondition.Message = &message
		return *buildCondition
	}

	for _, condition := range run.Status.Conditions {
		message = condition.Message
		if condition.Status == "False" {
			// only add the `check the logs...` information to unsuccessful builds
			message += fmt.Sprintf(". Check the logs with `toolforge build logs %s`", run.Name)
		}
		//NOTE: ORDER MATTERS
		if run.Status.CompletionTime == nil && condition.Status == "Unknown" {
			status = gen.BUILDRUNNING
		} else if condition.Status == "False" && condition.Reason == "PipelineRunCancelled" {
			status = gen.BUILDCANCELLED
		} else if condition.Status == "False" && condition.Reason == "PipelineRunTimeout" {
			status = gen.BUILDTIMEOUT
		} else if condition.Status == "False" {
			status = gen.BUILDFAILURE
		} else if condition.Status == "True" {
			status = gen.BUILDSUCCESS
		}
	}
	buildCondition.Status = &status
	buildCondition.Message = &message
	return *buildCondition
}

func getBuild(run v1beta1.PipelineRun) *gen.Build {
	var startTime string
	var endTime string
	buildCondition := getBuildConditionFromPipelineRun(&run)
	if run.Status.StartTime != nil {
		startTime = run.Status.StartTime.Format(time.RFC3339)
	}
	if run.Status.CompletionTime != nil {
		endTime = run.Status.CompletionTime.Format(time.RFC3339)
	}

	return &gen.Build{
		BuildId:   &run.Name,
		StartTime: &startTime,
		EndTime:   &endTime,
		Status:    buildCondition.Status,
		Message:   buildCondition.Message,
		Parameters: &gen.BuildParameters{
			SourceUrl: &run.Spec.Params[2].Value.StringVal,
			Ref:       &run.Spec.Params[3].Value.StringVal,
		},
		DestinationImage: &run.Spec.Params[1].Value.StringVal,
	}
}

func filterPipelineRunsByStatus(pipelineRuns []v1beta1.PipelineRun, filter gen.BuildStatus) []v1beta1.PipelineRun {
	var filteredPipelineRuns []v1beta1.PipelineRun
	for _, pipelineRun := range pipelineRuns {
		if *getBuildConditionFromPipelineRun(&pipelineRun).Status == filter {
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
	runningPipelineRuns := filterPipelineRunsByStatus(pipelineRuns, gen.BUILDRUNNING)
	successfulPipelineRuns := filterPipelineRunsByStatus(pipelineRuns, gen.BUILDSUCCESS)
	failedPipelineRuns := filterPipelineRunsByStatus(pipelineRuns, gen.BUILDFAILURE)
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

func getContainerLogs(clients *Clients, container string, taskRun *v1beta1.PipelineRunTaskRunStatus, namespace string) (string, error) {
	logs := clients.K8s.CoreV1().Pods(namespace).GetLogs(
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
				// As we changed the containers in the runs, some runs don't have the containers we look for
				if strings.HasPrefix(fmt.Sprintf("%s", err), fmt.Sprintf("container %s is not valid for pod", container)) {
					continue
				}
				return "", err
			}
			allLogs = append(allLogs, newLogs)
		}
		return strings.Join(allLogs, "\n"), nil
	}

	// TODO: check if we should instead show something more
	return "", nil
}

func Logs(api *BuildsApi, buildId string, toolName string) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}
	}

	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", buildId)})
	if err != nil {
		message := "Unable to find any pipelineruns! New installation?"
		return http.StatusNotFound, gen.NotFound{Message: &message}
	}

	if len(pipelineRuns) == 0 {
		message := fmt.Sprintf("Unable to find build with id '%s'", buildId)
		return http.StatusNotFound, gen.NotFound{Message: &message}

	} else if len(pipelineRuns) > 1 {
		message := fmt.Sprintf("Got %d builds matching name %s, only 1 was expected.", len(pipelineRuns), buildId)
		log.Warning(message)
		return http.StatusNotFound, gen.NotFound{Message: &message}
	}

	pipelineRun := pipelineRuns[0]
	logs, err := getPipelineRunLogs(&pipelineRun, &api.Clients, api.Config.BuildNamespace)
	if err != nil {
		message := fmt.Sprintf("Error getting the logs for %s: %s", buildId, err)
		log.Errorf(message)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	lines := strings.Split(logs, "\n")
	return http.StatusOK, gen.BuildLogs{
		Lines: &lines,
	}
}

func Start(
	api *BuildsApi,
	sourceURL string,
	ref string,
	toolName string,
) (int, interface{}) {
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
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	buildParams := gen.NewBuildParameters{
		Ref:       &ref,
		SourceUrl: &sourceURL,
	}
	return http.StatusOK, gen.NewBuild{
		Name:       &pipelineRun.Name,
		Parameters: &buildParams,
	}
}

func Delete(
	api *BuildsApi,
	buildId string,
	toolName string,
) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}
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
			return http.StatusNotFound, gen.NotFound{Message: &message}
		}

		log.Warnf(
			"Got error when deleting pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Unable to delete build!"
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	return http.StatusOK, gen.BuildId{Id: &buildId}
}

func Healthcheck(api *BuildsApi) (int, gen.HealthResponse) {
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
		}
	}

	message := "All systems normal"
	status := gen.HealthResponseStatus(gen.OK)
	return http.StatusOK, gen.HealthResponse{
		Message: &message,
		Status:  &status,
	}
}

func List(
	api *BuildsApi,
	toolName string,
) (int, interface{}) {
	log.Debugf("Listing builds: toolName=%s, namespace=%s", toolName, api.Config.BuildNamespace)
	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		message := fmt.Sprintf("Got error when listing %s's pipelineruns on namespace %s: %s", toolName, api.Config.BuildNamespace, err)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}
	log.Debugf("Found %d pipelineruns for %s", len(pipelineRuns), toolName)

	builds := make([]gen.Build, len(pipelineRuns))
	for i, run := range pipelineRuns {
		builds[i] = *getBuild(run)
	}
	return http.StatusOK, builds
}
