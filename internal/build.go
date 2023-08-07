package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func CreateHarborProjectForTool(api *BuildsApi, toolName string) error {
	harborProjectName, err := ToolNameToHarborProjectName(toolName)
	if err != nil {
		return err
	}
	log.Debugf("Attempting to create harbor project %s", harborProjectName)

	requestBody := map[string]interface{}{
		"project_name": harborProjectName,
		"public":       true,
		"registry_id":  nil,
	}
	requestBodyBytes, _ := json.Marshal(requestBody)
	url := fmt.Sprintf("%s/api/v2.0/projects", api.Config.HarborRepository)
	request, _ := http.NewRequest("POST", url, bytes.NewBuffer(requestBodyBytes))
	userAgent := "WMCS toolforge-builds-api Go-http-client"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	username := api.Config.HarborUsername
	password := api.Config.HarborPassword
	request.SetBasicAuth(username, password)

	response, err := api.Clients.Http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusCreated {
		log.Debugf("Created harbor project %s", harborProjectName)
		return nil
	}

	responseBody := make(map[string][]map[string]interface{})
	err = json.NewDecoder(response.Body).Decode(&responseBody)
	if err != nil {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf(string(body))
	}
	message := ""
	for _, err := range responseBody["errors"] {
		message += fmt.Sprintf("%s: %s\n", err["code"], err["message"])
	}

	if response.StatusCode == http.StatusConflict {
		log.Debug(message)
		return nil
	}
	return fmt.Errorf("failed to create harbor project: %s", message)
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
		if condition.Status == "Unknown" && run.Status.CompletionTime == nil {
			status = gen.BUILDRUNNING
		} else if condition.Status == "Unknown" && condition.Reason == "PipelineRunCancelled" {
			status = gen.BUILDCANCELLED
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

func getAllContainerLogs(clients *Clients, containers []string, podName string, namespace string) ([]string, error) {

	allLogs := make([]string, 0)
	for _, container := range containers {
		logs := clients.K8s.CoreV1().Pods(namespace).GetLogs(
			podName, &v1.PodLogOptions{
				Timestamps: true,
				Container:  container,
			},
		)
		rawLogs := []byte{}
		result := logs.Do(context.TODO())
		err := result.Error()

		if err == nil {
			rawLogs, err = result.Raw()
		}

		if err != nil {
			// As we changed the containers in the runs, some runs don't have the containers we look for
			if strings.HasPrefix(fmt.Sprintf("%s", err), fmt.Sprintf("container %s is not valid for pod", container)) {
				continue
			}
			return nil, err
		}

		stringLogs := string(rawLogs)
		logLines := make([]string, strings.Count("\n", stringLogs))
		for _, logLine := range strings.Split(stringLogs, "\n") {
			logLines = append(logLines, fmt.Sprintf("%s: %s", container, logLine))
		}

		allLogs = append(allLogs, strings.Join(logLines, "\n"))
	}
	return allLogs, nil
}

func getPipelineRunLogs(pipelineRun *v1beta1.PipelineRun, clients *Clients, namespace string) (string, error) {
	// TODO: retrieve also logs from pods that failed to start

	if !pipelineRun.HasStarted() {
		return "", nil
	}

	var allLogs []string
	for _, taskRun := range pipelineRun.Status.TaskRuns {
		containers, err := getContainersFromPod(
			clients,
			taskRun.Status.PodName,
			namespace,
		)
		if err != nil {
			return "", err
		}

		allLogs, err = getAllContainerLogs(clients, containers, taskRun.Status.PodName, namespace)
		if err != nil {
			return "", err
		}
	}
	return strings.Join(allLogs, "\n"), nil
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
	err := CreateHarborProjectForTool(api, toolName)
	if err != nil {
		message := fmt.Sprintf("Failed to create harbor project for tool %s: %s", toolName, err)
		log.Error(message)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}
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
						StringVal: fmt.Sprintf("%s/tool-%s/tool-%s:latest", strings.Split(api.Config.HarborRepository, "//")[1], toolName, toolName),
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

func Cancel(
	api *BuildsApi,
	buildId string,
	toolName string,
) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}
	}
	log.Debugf("Getting build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)
	pipelineRun, err := api.Clients.Tekton.TektonV1beta1().PipelineRuns(api.Config.BuildNamespace).Get(
		context.TODO(),
		buildId,
		metav1.GetOptions{},
	)
	if err != nil {
		if strings.HasSuffix(err.Error(), "not found") {
			message := fmt.Sprintf("Build with id %s not found", buildId)
			return http.StatusNotFound, gen.NotFound{Message: &message}
		}

		log.Warnf(
			"Got error when getting pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Error: Unable to cancel build!"
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	buildCondition := getBuildConditionFromPipelineRun(pipelineRun)

	switch *buildCondition.Status {
	case gen.BUILDSUCCESS, gen.BUILDFAILURE, gen.BUILDTIMEOUT:
		message := fmt.Sprintf("Build %s cannot be cancelled because it has already completed", buildId)
		log.Warnf(message)
		return http.StatusConflict, gen.Conflict{Message: &message}
	case gen.BUILDCANCELLED:
		message := fmt.Sprintf("Build %s cannot be cancelled again. It has already been cancelled", buildId)
		log.Warnf(message)
		return http.StatusConflict, gen.Conflict{Message: &message}
	}

	pipelineRun.Spec.Status = "Cancelled"

	log.Debugf("Updating build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)
	_, err = api.Clients.Tekton.TektonV1beta1().PipelineRuns(api.Config.BuildNamespace).Update(
		context.TODO(),
		pipelineRun,
		metav1.UpdateOptions{},
	)
	if err != nil {
		log.Warnf(
			"Got error when updating pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Unable to cancel build!"
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	// TODO: Figure out how to verify that the successful Update() call actually cancelled the build.
	// Right now we just assume it did and the only way to verify is to maybe poll for a few seconds?
	// Maybe check logs if it can be found there?
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
