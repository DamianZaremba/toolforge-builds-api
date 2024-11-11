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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	harborArtifact "github.com/goharbor/go-client/pkg/sdk/v2.0/client/artifact"
	harborProject "github.com/goharbor/go-client/pkg/sdk/v2.0/client/project"
	harborRepository "github.com/goharbor/go-client/pkg/sdk/v2.0/client/repository"
	harborModels "github.com/goharbor/go-client/pkg/sdk/v2.0/models"
	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
	tektonPipelineV1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/status"
	gen "gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Helper functions
func getContainersFromPod(client *Clients, podName string, namespace string) ([]string, error) {
	pod, err := client.K8s.CoreV1().Pods(namespace).Get(
		context.TODO(),
		podName,
		metav1.GetOptions{},
	)
	if err != nil {
		log.Debugf("Got error when getting pods with name '%s': %s", podName, err)
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

func ToolNameToHarborProjectName(toolName string) (string, error) {
	if !ToolforgeNameRegex.MatchString(toolName) {
		message := fmt.Sprintf("Name %s is not a valid toolforge tool name.", toolName)
		log.Error(message)
		return "", fmt.Errorf(message)
	}

	toolName = fmt.Sprintf("%s%s", HarborProjectPrefix, toolName)
	formattedName := ""
	prevChar := ""
	for _, char := range toolName {
		if strings.Contains(HarborSpecialChars, string(char)) && strings.Contains(HarborSpecialChars, prevChar) {
			formattedName += HarborSpecialCharFiller
		}
		formattedName += string(char)
		prevChar = string(char)
	}

	if !HarborNameRegex.MatchString(formattedName) {
		message := fmt.Sprintf("Formatted name %s is not a valid harbor project name.", formattedName)
		log.Error(message)
		return "", fmt.Errorf(message)
	}
	return formattedName, nil
}

func getNiceHarborError(err error) error {
	if strings.Contains(err.Error(), "timeout") {
		return fmt.Errorf("request to harbor timed out")
	}

	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Errorf("harbor connection refused")
	}

	if harborError, ok := err.(HarborError); ok {
		message := ""
		for _, err := range harborError.GetPayload().Errors {
			message += fmt.Sprintf("%v - %v\n", err.Code, err.Message)
		}
		return fmt.Errorf(message)
	}
	return fmt.Errorf("an unknown error occured")
}

func CreateHarborProjectForTool(api *BuildsApi, toolName string) error {
	harborProjectName, err := ToolNameToHarborProjectName(toolName)
	if err != nil {
		return err
	}
	log.Debugf("Attempting to create harbor project %s", harborProjectName)

	projectIsPublic := true
	projectDetails := &harborModels.ProjectReq{
		ProjectName: harborProjectName,
		Public:      &projectIsPublic,
		RegistryID:  nil,
	}
	newProject := harborProject.NewCreateProjectParams().WithProject(projectDetails)

	_, err = api.Clients.Harbor.V2().Project.CreateProject(context.TODO(), newProject)
	if err != nil {
		log.Error(err.Error())
		if _, ok := err.(*harborProject.CreateProjectConflict); ok {
			log.Debugf("harbor project %s already exists", harborProjectName)
			return nil
		}
		return getNiceHarborError(err)
	}

	log.Debugf("Created harbor project %s", harborProjectName)
	return nil
}

func getPipelineRuns(clients *Clients, namespace string, listoptions metav1.ListOptions) ([]tektonPipelineV1.PipelineRun, error) {

	pipelineRuns, err := clients.Tekton.TektonV1().PipelineRuns(namespace).List(
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

func getBuildConditionFromPipelineRun(run *tektonPipelineV1.PipelineRun) gen.BuildCondition {
	status := gen.BUILDUNKNOWN
	message := fmt.Sprintf("build status is unknown. Check the logs with `toolforge build logs %s`", run.Name)
	buildCondition := &gen.BuildCondition{
		Status:  &status,
		Message: &message,
	}

	if run.Status.Conditions == nil {
		return *buildCondition
	}

	for _, condition := range run.Status.Conditions {
		message = condition.Message

		if condition.Status == "True" {
			status = gen.BUILDSUCCESS
		} else if condition.Reason == "Running" {
			status = gen.BUILDRUNNING
		} else if condition.Status == "False" {
			status = gen.BUILDFAILURE
			// only add the `check the logs...` information to unsuccessful builds
			message += fmt.Sprintf(". Check the logs with `toolforge build logs %s`", run.Name)
		}

		if condition.Reason == "PipelineRunTimeout" {
			status = gen.BUILDTIMEOUT
		} else if condition.Reason == "PipelineRunCancelled" || // TODO: remove this line when we can safely assume legacy pipelineruns has been cleaned up
			condition.Reason == "Cancelled" ||
			condition.Reason == "CancelledRunFinally" ||
			condition.Reason == "StoppedRunFinally" {
			status = gen.BUILDCANCELLED
		}
	}

	buildCondition.Status = &status
	buildCondition.Message = &message
	return *buildCondition
}

func getpipelineRunStringParam(pipelineRun tektonPipelineV1.PipelineRun, name string) string {
	for _, param := range pipelineRun.Spec.Params {
		if param.Name == name {
			return fmt.Sprint(param.Value.StringVal)
		}
	}
	log.Warnf(
		"pipelineRunParam %s not found in pipelineRun %s in namespace %s", name, pipelineRun.Name, pipelineRun.Namespace,
	)
	return "unknown"
}

func getpipelineRunArrayParam(pipelineRun tektonPipelineV1.PipelineRun, name string) []string {
	for _, param := range pipelineRun.Spec.Params {
		if param.Name == name {
			return param.Value.ArrayVal
		}
	}
	log.Warnf(
		"pipelineRunParam %s not found in pipelineRun %s in namespace %s", name, pipelineRun.Name, pipelineRun.Namespace,
	)
	return nil
}

func getBuild(run tektonPipelineV1.PipelineRun) *gen.Build {
	var startTime string
	var endTime string
	buildCondition := getBuildConditionFromPipelineRun(&run)
	if run.Status.StartTime != nil {
		startTime = run.Status.StartTime.Format(time.RFC3339)
	}
	if run.Status.CompletionTime != nil {
		endTime = run.Status.CompletionTime.Format(time.RFC3339)
	}

	sourceurl := getpipelineRunStringParam(run, "SOURCE_URL")
	ref := getpipelineRunStringParam(run, "SOURCE_REFERENCE")
	destinationimage := getpipelineRunStringParam(run, "APP_IMAGE")
	envvarsStr := getpipelineRunArrayParam(run, "ENV_VARS")
	envvars := make(map[string]string)
	for _, envvarStr := range envvarsStr {
		parts := strings.SplitN(envvarStr, "=", 2)
		if len(parts) != 2 {
			log.Debugf("Got weird envvar string, ignoring (got %d parts): %s -> %v", len(parts), envvarStr, parts)
		} else {
			envvars[parts[0]] = parts[1]
		}
	}

	return &gen.Build{
		BuildId:   &run.Name,
		StartTime: &startTime,
		EndTime:   &endTime,
		Status:    buildCondition.Status,
		Message:   buildCondition.Message,
		Parameters: &gen.BuildParameters{
			SourceUrl: sourceurl,
			Ref:       &ref,
			Envvars:   &envvars,
		},
		DestinationImage: &destinationimage,
	}
}

func filterPipelineRunsByStatus(pipelineRuns []tektonPipelineV1.PipelineRun, filter gen.BuildStatus) []tektonPipelineV1.PipelineRun {
	var filteredPipelineRuns []tektonPipelineV1.PipelineRun
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
	pipelineRunsToKeep := map[string]tektonPipelineV1.PipelineRun{}
	for _, pipelineRun := range runningPipelineRuns {
		pipelineRunsToKeep[pipelineRun.Name] = pipelineRun
	}
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
			err := clients.Tekton.TektonV1().PipelineRuns(namespace).Delete(
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

func sendLine(ctx echo.Context, line string) error {
	err := json.NewEncoder(ctx.Response()).Encode(gen.BuildLog{Line: &line})
	if err != nil {
		log.Debugf("Error encoding log line: %s", err)
		return err
	}
	ctx.Response().Flush()
	return nil
}

func StreamLogsForContainer(ctx echo.Context, container string, logs map[string]io.ReadCloser, buffers map[string][]byte) error {
	partialLogLine := "" // used to handle fractional lines (i.e lines that are not terminated by a newline)
	for {
		n, err := logs[container].Read(buffers[container])
		if n > 0 {
			logLines := strings.Split(fmt.Sprintf("%s%s", partialLogLine, string(buffers[container][:n])), "\n")
			partialLogLine = logLines[len(logLines)-1]
			logLines = logLines[:len(logLines)-1]
			for _, logLine := range logLines {
				logLine = fmt.Sprintf("[%s] %s", container, logLine) // prepend container name to log line
				err := sendLine(ctx, logLine)
				if err != nil {
					return err
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if partialLogLine != "" {
					err := sendLine(ctx, fmt.Sprintf("[%s] %s", container, partialLogLine))
					if err != nil {
						return err
					}
				}
				break
			}
			log.Debugf("Error reading logs: %s", err)
			return err
		}
	}
	return nil
}

func streamAllContainerLogs(ctx echo.Context, clients *Clients, containers []string, podName string, namespace string, follow bool) error {
	logs := map[string]io.ReadCloser{}
	containerLogBuffers := make(map[string][]byte)
	for _, container := range containers {
		containerLog, err := clients.K8s.CoreV1().Pods(namespace).GetLogs(
			podName,
			&v1.PodLogOptions{
				Container:  container,
				Timestamps: true,
				Follow:     follow,
			},
		).Stream(context.Background())
		if err != nil {
			// As we changed the containers in the runs, some runs don't have the containers we look for
			if strings.HasPrefix(fmt.Sprintf("%s", err), fmt.Sprintf("container %s is not valid for pod", container)) {
				logs[container] = nil
				continue
			}
			return err
		}
		defer containerLog.Close()
		logs[container] = containerLog
		containerLogBuffers[container] = make([]byte, 1024)
	}
	for _, container := range containers {
		if logs[container] == nil {
			continue
		}
		err := StreamLogsForContainer(ctx, container, logs, containerLogBuffers)
		if err != nil {
			return err
		}
	}
	return nil
}

// This is to have backwards compatibility with old v1beta1 taskRuns, once we migrate all of them (as they get
// recycled), we can remove this chunk of code
func getPodsRunsV1beta1(clients *Clients, namespace string, pipelineRun *tektonPipelineV1.PipelineRun) (error, []string) {
	pipelineRunv1beta1, err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).Get(context.TODO(), pipelineRun.Name, metav1.GetOptions{})
	if err != nil {
		return err, nil
	}

	pods := make([]string, 0, len(pipelineRunv1beta1.Status.TaskRuns))
	for _, taskRun := range pipelineRunv1beta1.Status.TaskRuns {
		pods = append(pods, taskRun.Status.PodName)
	}

	return nil, pods
}

func streamPipelineRunLogs(ctx echo.Context, clients *Clients, namespace string, pipelineRun *tektonPipelineV1.PipelineRun, follow bool) error {
	// TODO: retrieve also logs from pods that failed to start
	if !pipelineRun.HasStarted() {
		return fmt.Errorf(PipelineRunNotStartedErrorStr)
	}

	taskRunStatuses, _, err := status.GetPipelineTaskStatuses(ctx.Request().Context(), clients.Tekton, namespace, pipelineRun)
	if err != nil {
		log.Debugf("Got error when getting task statuses for the pipeline %v: %s", pipelineRun, err)
		return err
	}

	pods := make([]string, 0, len(taskRunStatuses))
	if len(taskRunStatuses) == 0 {
		// We might be dealing with an old taskRun v1beta1, instead of v1
		// this can be removed once we know all the pipelineRuns are on the v1 version
		log.Debug("Got no taskRunStatuses, trying with v1beta1")
		err, pods = getPodsRunsV1beta1(clients, namespace, pipelineRun)
		if err != nil {
			log.Debugf("Got error when getting task statuses for the pipeline %v: %s", pipelineRun, err)
			return err
		}

		if len(pods) == 0 {
			return fmt.Errorf("got no taskruns for this pipeline, something weird is going on: %v", pipelineRun)
		}
		log.Debugf("Got no taskrun for the pipeline %v", pipelineRun)
	} else {
		for _, taskRun := range taskRunStatuses {
			pods = append(pods, taskRun.Status.PodName)
		}
	}

	log.Debugf("Got %d pods for the pipeline %v: %v", len(pods), pipelineRun, pods)
	for _, podName := range pods {
		log.Debugf("Getting container for pod '%s'", podName)
		containers, err := getContainersFromPod(
			clients,
			podName,
			namespace,
		)
		if err != nil {
			log.Debugf("Got error when getting containers for pod %v: %s", podName, err)
			return err
		}
		err = streamAllContainerLogs(ctx, clients, containers, podName, namespace, follow)
		if err != nil {
			log.Debugf("Got error when streaming logs from container %v: %s", containers, err)
			return err
		}
	}
	log.Debugf("Finished streaming pipelinerun logs")
	return nil
}

func StreamAfterPipelineRunStarted(ctx echo.Context, clients *Clients, namespace string, follow bool, listoptions metav1.ListOptions, timeout time.Duration) error {
	start_time := time.Now()
	current_time := start_time

	for current_time.Sub(start_time) < timeout {
		// we need get the pipelinerun in each loop or we will get a stale object
		pipelineRun, err := getPipelineRuns(clients, namespace, listoptions)
		if err != nil || len(pipelineRun) < 1 {
			message := "unable to find any pipelineruns! New installation?: %v"
			return fmt.Errorf(message, pipelineRun)
		}
		err = streamPipelineRunLogs(ctx, clients, namespace, &pipelineRun[0], follow)
		if err == nil {
			return nil
		}
		if !follow {
			return err
		}
		if !strings.Contains(err.Error(), PipelineRunNotStartedErrorStr) &&
			!strings.Contains(err.Error(), ResourceNameEmptyErrorStr) &&
			!strings.Contains(err.Error(), PodInitializingErrorStr) {
			return err
		}
		time.Sleep(1 * time.Second)
		current_time = time.Now()
	}
	return fmt.Errorf("timed out waiting for pipelinerun to start")
}

func Logs(ctx echo.Context, api *BuildsApi, buildId string, toolName string, follow bool) (int, interface{}) {
	waitTimeout := 10 * time.Minute

	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.ResponseMessages{Error: &[]string{message}}
	}

	listoptions := metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", buildId)}
	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, listoptions)
	if err != nil {
		message := "Unable to find any pipelineruns! New installation?"
		return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}
	}

	if len(pipelineRuns) == 0 {
		message := fmt.Sprintf("Unable to find build with id '%s'", buildId)
		return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}

	} else if len(pipelineRuns) > 1 {
		message := fmt.Sprintf("Got %d builds matching name %s, only 1 was expected.", len(pipelineRuns), buildId)
		log.Warning(message)
		return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}
	}

	err = StreamAfterPipelineRunStarted(ctx, &api.Clients, api.Config.BuildNamespace, follow, listoptions, waitTimeout)
	if err != nil {
		message := fmt.Sprintf("Error getting the logs for %s: %s", buildId, err)
		log.Errorf(message)
		// Note that when streaming, once the first line is sent, you can't really change the http error code,
		// so this internal server error is only effective if we did not yet send any data.
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	return http.StatusOK, nil
}

func formatBytes(bytes int64) string {
	units := []string{"Ti", "Gi", "Mi", "Ki", "B"}
	values := []int64{1 << 40, 1 << 30, 1 << 20, 1 << 10, 1}

	for i, unit := range units {
		if bytes >= values[i] {
			return fmt.Sprintf("%.2f%s", float64(bytes)/float64(values[i]), unit)
		}
	}

	return fmt.Sprintf("%dB", bytes)
}

func cleanHarbor(api *BuildsApi, toolName string) (gen.CleanResponse, error) {
	harborProjectName, err := ToolNameToHarborProjectName(toolName)
	if err != nil {
		return gen.CleanResponse{}, fmt.Errorf("error trying to map tool %s to harbor project: %s", toolName, err)
	}

	pageSize := int64(-1)
	response, err := api.Clients.Harbor.V2().Repository.ListRepositories(
		context.TODO(),
		&harborRepository.ListRepositoriesParams{ProjectName: harborProjectName, PageSize: &pageSize},
	)
	if err != nil {
		log.Error(err.Error())
		if _, ok := err.(*harborRepository.ListRepositoriesNotFound); ok {
			return gen.CleanResponse{}, fmt.Errorf(
				"the project %s does not exist, have you started a build yet?",
				harborProjectName,
			)
		}

		return gen.CleanResponse{}, fmt.Errorf(
			"error trying to get the repositories for project %s: %s",
			harborProjectName,
			getNiceHarborError(err),
		)
	}

	message := ""
	repositories := response.Payload
	for _, repository := range repositories {
		// for some reason harbor returns the name bing <project>/<repository>, but then requires you to use only the last part for any queries
		log.Infof("Got repo: %v", repository.Name)
		repoName := strings.SplitN(repository.Name, "/", 2)[1]
		pageSize := int64(-1)
		response, err := api.Clients.Harbor.V2().Artifact.ListArtifacts(
			context.TODO(),
			&harborArtifact.ListArtifactsParams{
				ProjectName:    harborProjectName,
				RepositoryName: repoName,
				PageSize:       &pageSize,
			},
		)
		if err != nil {
			log.Error(err.Error())
			return gen.CleanResponse{}, fmt.Errorf(
				"error trying to get the artifacts for project %s: %s",
				harborProjectName,
				getNiceHarborError(err),
			)
		}

		artifacts := response.Payload
		for _, artifact := range artifacts {
			// this is neccessary because our harbor-client returns [<object with empty fields>]
			// when harbor server returns empty array ([])
			if artifact.Digest == "" {
				continue
			}
			_, err := api.Clients.Harbor.V2().Artifact.DeleteArtifact(
				context.TODO(),
				&harborArtifact.DeleteArtifactParams{
					ProjectName:    harborProjectName,
					RepositoryName: repoName,
					Reference:      artifact.Digest,
				},
			)
			if err != nil {
				log.Error(err.Error())
				return gen.CleanResponse{}, fmt.Errorf(
					"error trying to delete artifact %s from project %s: %s",
					artifact.Digest,
					harborProjectName,
					getNiceHarborError(err),
				)
			}
		}
		message += fmt.Sprintf("Deleted %d artifacts from harbor repository %s/%s\n", len(artifacts), harborProjectName, repoName)
	}
	if message == "" {
		message = "Nothing to clean up"
	}

	return gen.CleanResponse{
		Messages: &gen.ResponseMessages{
			Info: &[]string{message},
		},
	}, nil
}

func checkIfHarborProjectExists(api *BuildsApi, projectName string) (bool, error) {
	_, err := api.Clients.Harbor.V2().Project.HeadProject(
		context.TODO(),
		&harborProject.HeadProjectParams{ProjectName: projectName},
	)

	if err != nil {
		if _, ok := err.(*harborProject.HeadProjectNotFound); ok {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func GetHarborQuota(api *BuildsApi, toolName string) (gen.Quota, error) {
	harborProjectName, err := ToolNameToHarborProjectName(toolName)
	if err != nil {
		return gen.Quota{}, err
	}

	projectExists, err := checkIfHarborProjectExists(api, harborProjectName)
	if err != nil {
		return gen.Quota{}, err
	}

	if !projectExists {
		return gen.Quota{}, fmt.Errorf("Quota cannot be displayed because no builds have run yet")
	}

	response, err := api.Clients.Harbor.V2().Project.GetProjectSummary(
		context.TODO(),
		&harborProject.GetProjectSummaryParams{ProjectNameOrID: harborProjectName},
	)

	if err != nil {
		log.Error(err.Error())
		return gen.Quota{}, fmt.Errorf("failed to get harbor quota: %s", getNiceHarborError(err))
	}

	quota := response.Payload.Quota

	storageHard := quota.Hard["storage"]
	storageUsed := quota.Used["storage"]

	var storageHardStr, storageAvailableStr, storageCapacityStr string
	if storageHard == -1 {
		storageHardStr = "Unlimited"
		storageAvailableStr = "Unlimited"
		storageCapacityStr = "0%"
	} else {
		storageAvailable := storageHard - storageUsed
		storageCapacity := (float64(storageUsed) / float64(storageHard)) * 100

		storageHardStr = formatBytes(storageHard)
		storageAvailableStr = formatBytes(storageAvailable)
		storageCapacityStr = fmt.Sprintf("%.0f%%", storageCapacity)
	}

	storageUsedStr := formatBytes(storageUsed)

	category := "Registry"
	itemName := "Storage"
	res := gen.Quota{
		Categories: &[]gen.QuotaCategory{
			{
				Name: &category,
				Items: &[]gen.QuotaCategoryItem{
					{Name: &itemName, Limit: &storageHardStr, Used: &storageUsedStr, Available: &storageAvailableStr, Capacity: &storageCapacityStr},
				},
			},
		},
	}

	return res, nil
}

func ValidateEnvvars(envvars map[string]string) error {
	varnameRegex := "^[A-Z_][A-Z_0-9]{2,}$"
	varnameRegexCompiled, err := regexp.Compile(varnameRegex)
	if err != nil {
		return err
	}

	for varname := range envvars {
		match := varnameRegexCompiled.MatchString(varname)
		if !match {
			return fmt.Errorf("not valid environment variable name, must match '%s', got '%s'", varnameRegex, varname)
		}
	}
	return nil
}

// Handler functions
func Start(
	api *BuildsApi,
	sourceURL string,
	ref string,
	imageName string,
	toolName string,
	envvars map[string]string,
) (int, interface{}) {
	err := ValidateEnvvars(envvars)
	if err != nil {
		message := fmt.Sprintf("Not valid environment variables passed: %s", err)
		return http.StatusBadRequest, gen.ResponseMessages{Error: &[]string{message}}
	}

	err = CreateHarborProjectForTool(api, toolName)
	if err != nil {
		message := fmt.Sprintf("Failed to create harbor project for tool %s: %s", toolName, err)
		return http.StatusServiceUnavailable, gen.ResponseMessages{Error: &[]string{message}}
	}
	cleanup_err := cleanupOldPipelineRuns(&api.Clients, api.Config.BuildNamespace, toolName, api.Config.OkToKeep, api.Config.FailedToKeep)
	for _, err := range cleanup_err {
		log.Warnf("Got error when cleaning up old pipeline runs: %s", err)
	}
	imageNameToUse := imageName
	if imageNameToUse == "" {
		imageNameToUse = fmt.Sprintf("tool-%s", toolName)
	}
	envvarsArray := make([]string, 0, len(envvars))
	for varname, value := range envvars {
		envvarsArray = append(envvarsArray, fmt.Sprintf("%s=%s", varname, value))
	}
	log.Debugf("Starting a new build: ref=%s, imageName=%s, toolName=%s, harborRepository=%s, builder=%s", ref, imageName, toolName, api.Config.HarborRepository, api.Config.Builder)
	newRun := tektonPipelineV1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s%s", toolName, api.Config.BuildIdPrefix),
			Namespace:    api.Config.BuildNamespace,
			Labels: map[string]string{
				"user": toolName,
			},
		},
		Spec: tektonPipelineV1.PipelineRunSpec{
			PipelineRef: &tektonPipelineV1.PipelineRef{Name: "buildpacks"},
			// The docs say that you can use the serviceAccountName field in the PipelineRun, but there's no such field,
			// so we use the taskRunSpec instead
			TaskRunSpecs: []tektonPipelineV1.PipelineTaskRunSpec{
				{
					PipelineTaskName:   "build-from-git",
					ServiceAccountName: "buildpacks-service-account",
				},
			},
			Params: []tektonPipelineV1.Param{
				{
					Name: "BUILDER_IMAGE",
					Value: tektonPipelineV1.ParamValue{
						StringVal: api.Config.Builder,
						Type:      tektonPipelineV1.ParamTypeString,
					},
				},
				{
					Name: "APP_IMAGE",
					Value: tektonPipelineV1.ParamValue{
						StringVal: fmt.Sprintf("%s/tool-%s/%s:latest", strings.Split(api.Config.HarborRepository, "//")[1], toolName, imageNameToUse),
						Type:      tektonPipelineV1.ParamTypeString,
					},
				},
				{
					Name:  "SOURCE_URL",
					Value: tektonPipelineV1.ParamValue{StringVal: sourceURL, Type: tektonPipelineV1.ParamTypeString},
				},
				{
					Name:  "SOURCE_REFERENCE",
					Value: tektonPipelineV1.ParamValue{StringVal: ref, Type: tektonPipelineV1.ParamTypeString},
				},
				{
					Name:  "ENV_VARS",
					Value: tektonPipelineV1.ParamValue{ArrayVal: envvarsArray, Type: tektonPipelineV1.ParamTypeArray},
				},
			},
			Workspaces: []tektonPipelineV1.WorkspaceBinding{
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

	pipelineRun, err := api.Clients.Tekton.TektonV1().PipelineRuns(api.Config.BuildNamespace).Create(
		context.TODO(),
		&newRun,
		metav1.CreateOptions{},
	)
	if err != nil {
		message := fmt.Sprintf("Got error when creating a new pipelinerun on namespace %s: %s", api.Config.BuildNamespace, err)
		log.Warn(message)
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	var responseMessages gen.ResponseMessages
	quota, err := GetHarborQuota(api, toolName)
	if err != nil {
		log.Errorf("Got error while trying to get harbor quota for tool %s: %s", toolName, err)
	} else {
		quotaCapacityStr := (*(*quota.Categories)[0].Items)[0].Capacity
		quotaCapacity, err := strconv.ParseFloat(strings.TrimSuffix(*quotaCapacityStr, "%"), 64)
		if err != nil {
			log.Errorf("Got error while trying to parse harbor quota capacity for tool %s: %s", toolName, err)
		} else if quotaCapacity > 90 {
			// if the capacity is over 90%, we should warn the user
			message := fmt.Sprintf(
				"Warning: Tool %s has used up %s of it's alloted quota."+
					"To avoid the possibility of your build failing, run \"toolforge build clean\" to free up quota.",
				toolName,
				*quotaCapacityStr,
			)
			responseMessages = gen.ResponseMessages{Warning: &[]string{message}}
		}
	}

	buildParams := gen.BuildParameters{
		SourceUrl: sourceURL,
		Ref:       &ref,
		Envvars:   &envvars,
	}
	newBuild := gen.NewBuild{
		Name:       &pipelineRun.Name,
		Parameters: &buildParams,
	}
	return http.StatusOK, gen.StartResponse{
		NewBuild: &newBuild,
		Messages: &responseMessages,
	}
}

func Delete(
	api *BuildsApi,
	buildId string,
	toolName string,
) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.ResponseMessages{Error: &[]string{message}}
	}
	// TODO: Delete also the associated image on harbor
	log.Debugf("Deleting build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)
	err := api.Clients.Tekton.TektonV1().PipelineRuns(api.Config.BuildNamespace).Delete(
		context.TODO(),
		buildId,
		metav1.DeleteOptions{},
	)
	if err != nil {
		// A bit flaky way of handling, maybe improve in the future
		if strings.HasSuffix(err.Error(), "not found") {
			message := fmt.Sprintf("Build with id %s not found", buildId)
			return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}
		}

		log.Warnf(
			"Got error when deleting pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Unable to delete build!"
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	return http.StatusOK, gen.DeleteResponse{Id: &buildId, Messages: &gen.ResponseMessages{}}
}

func Get(
	api *BuildsApi,
	id string,
	toolName string,
) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, id, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.ResponseMessages{Error: &[]string{message}}
	}
	log.Debugf("Getting build: buildId=%s, namespace=%s, toolName=%s", id, api.Config.BuildNamespace, toolName)

	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", id)})
	if err != nil {
		log.Warnf(
			"Got error when getting pipelinerun %s on namespace %s: %s", id, api.Config.BuildNamespace, err,
		)
		message := "Unable to get build! This might be a bug. Please contact a Toolforge admin."
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	if len(pipelineRuns) == 0 {
		message := fmt.Sprintf("Build with id %s not found.", id)
		return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}
	}

	// NOTE: we assume here the first pipelineRun from the search is what we are looking for.
	// In k8s/tekton two objects cannot share metadata.name in the same namespace anyway

	build := getBuild(pipelineRuns[0])
	return http.StatusOK, gen.GetResponse{Build: build, Messages: &gen.ResponseMessages{}}
}

func List(
	api *BuildsApi,
	toolName string,
) (int, interface{}) {
	log.Debugf("Listing builds: toolName=%s, namespace=%s", toolName, api.Config.BuildNamespace)
	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		message := fmt.Sprintf("Got error when listing %s's pipelineruns on namespace %s: %s", toolName, api.Config.BuildNamespace, err)
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}
	log.Debugf("Found %d pipelineruns for %s", len(pipelineRuns), toolName)

	builds := make([]gen.Build, len(pipelineRuns))
	for i, run := range pipelineRuns {
		builds[i] = *getBuild(run)
	}
	return http.StatusOK, gen.ListResponse{Builds: &builds, Messages: &gen.ResponseMessages{}}
}

func Latest(
	api *BuildsApi,
	toolName string,
) (int, interface{}) {
	log.Debugf("Getting latest build: namespace=%s, toolName=%s", api.Config.BuildNamespace, toolName)

	pipelineRuns, err := getPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		log.Warnf(
			"Got error when getting latest pipelineruns on namespace %s: %s", api.Config.BuildNamespace, err,
		)
		message := "Unable to get build! This might be a bug. Please contact a Toolforge admin."
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	if len(pipelineRuns) == 0 {
		message := "No builds exist yet."
		return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}
	}

	// getPipelineRuns returns a sorted array per creationTimestamp
	build := getBuild(pipelineRuns[0])
	return http.StatusOK, gen.LatestResponse{Build: build, Messages: &gen.ResponseMessages{}}
}

func Cancel(
	api *BuildsApi,
	buildId string,
	toolName string,
) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.ResponseMessages{Error: &[]string{message}}
	}
	log.Debugf("Getting build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)
	pipelineRun, err := api.Clients.Tekton.TektonV1().PipelineRuns(api.Config.BuildNamespace).Get(
		context.TODO(),
		buildId,
		metav1.GetOptions{},
	)
	if err != nil {
		if strings.HasSuffix(err.Error(), "not found") {
			message := fmt.Sprintf("Build with id %s not found", buildId)
			return http.StatusNotFound, gen.ResponseMessages{Error: &[]string{message}}
		}

		log.Warnf(
			"Got error when getting pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Error: Unable to cancel build!"
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	buildCondition := getBuildConditionFromPipelineRun(pipelineRun)

	switch *buildCondition.Status {
	case gen.BUILDSUCCESS, gen.BUILDFAILURE, gen.BUILDTIMEOUT:
		message := fmt.Sprintf("Build %s cannot be cancelled because it has already completed", buildId)
		log.Warnf(message)
		return http.StatusConflict, gen.ResponseMessages{Error: &[]string{message}}
	case gen.BUILDCANCELLED:
		message := fmt.Sprintf("Build %s cannot be cancelled again. It has already been cancelled", buildId)
		log.Warnf(message)
		return http.StatusConflict, gen.ResponseMessages{Error: &[]string{message}}
	}

	pipelineRun.Spec.Status = "Cancelled"

	log.Debugf("Updating build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)
	_, err = api.Clients.Tekton.TektonV1().PipelineRuns(api.Config.BuildNamespace).Update(
		context.TODO(),
		pipelineRun,
		metav1.UpdateOptions{},
	)
	if err != nil {
		log.Warnf(
			"Got error when updating pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Unable to cancel build!"
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	return http.StatusOK, gen.CancelResponse{Id: &buildId, Messages: &gen.ResponseMessages{}}
}

func Quota(api *BuildsApi, toolName string) (int, interface{}) {
	quota, err := GetHarborQuota(api, toolName)
	if err != nil {
		log.Error(err)
		message := fmt.Sprintf("Error getting quota from Harbor: %s", err)
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	return http.StatusOK, gen.QuotaResponse{
		Quota:    &gen.Quota{Categories: quota.Categories},
		Messages: &gen.ResponseMessages{},
	}
}

func Clean(api *BuildsApi, toolName string) (int, interface{}) {
	cleanResponse, err := cleanHarbor(api, toolName)
	if err != nil {
		log.Error(err)
		message := fmt.Sprintf("Error cleaning up: %s", err)
		return http.StatusInternalServerError, gen.ResponseMessages{Error: &[]string{message}}
	}

	return http.StatusOK, cleanResponse
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
			Messages: &gen.ResponseMessages{
				Info: &[]string{message},
			},
			Status: &status,
		}
	}

	message := "All systems normal"
	status := gen.HealthResponseStatus(gen.OK)
	return http.StatusOK, gen.HealthResponse{
		Messages: &gen.ResponseMessages{
			Info: &[]string{message},
		},
		Status: &status,
	}
}
