package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	gen "gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Helper functions
func GetContainersFromPod(client *Clients, podName string, namespace string) ([]string, error) {
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

func DoHarborRequest(api *BuildsApi, method string, url string) (http.Response, interface{}, error) {
	var jsonBody interface{}
	full_url := fmt.Sprintf("%s/api/v2.0%s", api.Config.HarborRepository, url)

	request, err := http.NewRequest(method, full_url, nil)
	if err != nil {
		return http.Response{}, jsonBody, err
	}
	userAgent := "WMCS toolforge-builds-api Go-http-client"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	username := api.Config.HarborUsername
	password := api.Config.HarborPassword
	request.SetBasicAuth(username, password)

	response, err := api.Clients.Http.Do(request)
	if err != nil {
		log.Error(err.Error())
		err = HandleConnectionErrors(err)
		return http.Response{}, jsonBody, err
	}
	defer response.Body.Close()

	rawBody, _ := io.ReadAll(response.Body)
	log.Debugf("Raw response body: %s", string(rawBody))
	log.Debugf("Response status code: %d", response.StatusCode)
	log.Debugf("Response headers: %v", response.Header)

	// some requests return nothing
	if string(rawBody) == "" {
		if response.StatusCode >= 400 {
			return *response, jsonBody, fmt.Errorf("got error code %d", response.StatusCode)
		}
		return *response, jsonBody, nil
	}

	err = json.NewDecoder(bytes.NewBuffer(rawBody)).Decode(&jsonBody)
	if err != nil {
		return *response, jsonBody, fmt.Errorf("%s - body: %s", err, string(rawBody))
	}

	// sometimes harbor replies with error structures, this tries to check if that's the case
	// note that we have to unwrap the types one layer at a time, carrying the interface{}
	switch val := jsonBody.(type) {
	case map[string]interface{}:
		errors, gotErrors := val["errors"]
		if gotErrors {
			message := ""
			for _, err := range errors.([]interface{}) {
				message += fmt.Sprintf("%v: %v\n", err.(map[string]interface{})["code"], err.(map[string]interface{})["message"])
			}
			if message != "" {
				return *response, jsonBody, fmt.Errorf("got errors: %s", message)
			}
		}
	}

	return *response, jsonBody, nil
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
		log.Error(err.Error())
		// handle connection errors and timeouts
		// TODO: Use HandleConnectionErrors() for this
		netErr, ok := err.(net.Error)
		if ok && netErr.Timeout() {
			return fmt.Errorf("request to harbor timed out")
		} else if strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("harbor connection refused")
		} else {
			return err
		}
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

func GetPipelineRuns(clients *Clients, namespace string, listOptions metav1.ListOptions) ([]v1beta1.PipelineRun, error) {

	pipelineRuns, err := clients.Tekton.TektonV1beta1().PipelineRuns(namespace).List(
		context.TODO(),
		listOptions,
	)
	if err != nil {
		log.Warnf(
			"Got error when listing pipelineruns on namespace %s (%v), maybe new cluster with no runs yet?: %s", namespace, listOptions, err,
		)
		return nil, err
	}
	sort.Slice(pipelineRuns.Items, func(i, j int) bool {
		return !pipelineRuns.Items[i].CreationTimestamp.Before(&pipelineRuns.Items[j].CreationTimestamp)
	})
	return pipelineRuns.Items, nil
}

func GetBuildConditionFromPipelineRun(run *v1beta1.PipelineRun) gen.BuildCondition {
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

func GetPipelineRunStringParam(pipelineRun v1beta1.PipelineRun, name string) string {
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

func GetPipelineRunArrayParam(pipelineRun v1beta1.PipelineRun, name string) []string {
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

func GetBuild(run v1beta1.PipelineRun) *gen.Build {
	var startTime string
	var endTime string
	buildCondition := GetBuildConditionFromPipelineRun(&run)
	if run.Status.StartTime != nil {
		startTime = run.Status.StartTime.Format(time.RFC3339)
	}
	if run.Status.CompletionTime != nil {
		endTime = run.Status.CompletionTime.Format(time.RFC3339)
	}

	sourceurl := GetPipelineRunStringParam(run, "SOURCE_URL")
	ref := GetPipelineRunStringParam(run, "SOURCE_REFERENCE")
	destinationimage := GetPipelineRunStringParam(run, "APP_IMAGE")
	envvarsStr := GetPipelineRunArrayParam(run, "ENV_VARS")
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
			SourceUrl: &sourceurl,
			Ref:       &ref,
			Envvars:   &envvars,
		},
		DestinationImage: &destinationimage,
	}
}

func FilterPipelineRunsByStatus(pipelineRuns []v1beta1.PipelineRun, filter gen.BuildStatus) []v1beta1.PipelineRun {
	var filteredPipelineRuns []v1beta1.PipelineRun
	for _, pipelineRun := range pipelineRuns {
		if *GetBuildConditionFromPipelineRun(&pipelineRun).Status == filter {
			filteredPipelineRuns = append(filteredPipelineRuns, pipelineRun)
		}
	}
	return filteredPipelineRuns
}

func CleanupOldPipelineRuns(clients *Clients, namespace string, toolName string, okToKeep int, failedToKeep int) []error {
	pipelineRuns, err := GetPipelineRuns(clients, namespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		return []error{err}
	}
	log.Debugf("Found %d pipelineruns. Cleaning up old runs...", len(pipelineRuns))
	runningPipelineRuns := FilterPipelineRunsByStatus(pipelineRuns, gen.BUILDRUNNING)
	successfulPipelineRuns := FilterPipelineRunsByStatus(pipelineRuns, gen.BUILDSUCCESS)
	failedPipelineRuns := FilterPipelineRunsByStatus(pipelineRuns, gen.BUILDFAILURE)
	pipelineRunsToKeep := map[string]v1beta1.PipelineRun{}
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

func SendLine(ctx echo.Context, line string) error {
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
				err := SendLine(ctx, logLine)
				if err != nil {
					return err
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if partialLogLine != "" {
					err := SendLine(ctx, fmt.Sprintf("[%s] %s", container, partialLogLine))
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

func StreamAllContainerLogs(ctx echo.Context, clients *Clients, containers []string, podName string, namespace string, follow bool) error {
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

func StreamPipelineRunLogs(ctx echo.Context, clients *Clients, namespace string, pipelineRun *v1beta1.PipelineRun, follow bool) error {
	// TODO: retrieve also logs from pods that failed to start
	if !pipelineRun.HasStarted() {
		return fmt.Errorf(PipelineRunNotStartedErrorStr)
	}
	for _, taskRun := range pipelineRun.Status.TaskRuns {
		containers, err := GetContainersFromPod(
			clients,
			taskRun.Status.PodName,
			namespace,
		)
		if err != nil {
			return err
		}
		err = StreamAllContainerLogs(ctx, clients, containers, taskRun.Status.PodName, namespace, follow)
		if err != nil {
			return err
		}
	}
	return nil
}

func StreamAfterPipelineRunStarted(ctx echo.Context, clients *Clients, namespace string, follow bool, listOptions metav1.ListOptions, timeout time.Duration) error {
	startTime := time.Now()
	currentTime := startTime

	for currentTime.Sub(startTime) < timeout {
		// we need get the pipelinerun in each loop or we will get a stale object
		pipelineRun, err := GetPipelineRuns(clients, namespace, listOptions)
		if err != nil {
			message := "unable to find any pipelineruns! New installation?"
			return fmt.Errorf(message)
		}
		err = StreamPipelineRunLogs(ctx, clients, namespace, &pipelineRun[0], follow)
		if err == nil {
			return nil
		}
		if err != nil {
			if !follow {
				return err
			}
			if !strings.Contains(err.Error(), PipelineRunNotStartedErrorStr) &&
				!strings.Contains(err.Error(), ResourceNameEmptyErrorStr) &&
				!strings.Contains(err.Error(), PodInitializingErrorStr) {
				return err
			}
		}
		time.Sleep(1 * time.Second)
		currentTime = time.Now()
	}
	return fmt.Errorf("timed out waiting for pipelinerun to start")
}

func HandleConnectionErrors(err error) error {
	netErr, ok := err.(net.Error)
	if ok && netErr.Timeout() {
		return fmt.Errorf("request to harbor timed out")
	} else if strings.Contains(err.Error(), "connection refused") {
		return fmt.Errorf("harbor connection refused")
	}
	return err
}

func FormatBytes(bytes int64) string {
	units := []string{"Ti", "Gi", "Mi", "Ki", "B"}
	values := []int64{1 << 40, 1 << 30, 1 << 20, 1 << 10, 1}

	for i, unit := range units {
		if bytes >= values[i] {
			return fmt.Sprintf("%.2f%s", float64(bytes)/float64(values[i]), unit)
		}
	}

	return fmt.Sprintf("%dB", bytes)
}

func CleanHarbor(api *BuildsApi, toolName string) (gen.CleanResponse, error) {
	harborProjectName, err := ToolNameToHarborProjectName(toolName)
	if err != nil {
		return gen.CleanResponse{}, fmt.Errorf("error trying to map tool %s to harbor project: %s", toolName, err)
	}

	response, jsonBody, err := DoHarborRequest(api, "GET", fmt.Sprintf("/projects/%s/repositories", harborProjectName))
	if err != nil {
		if response.StatusCode == 404 {
			return gen.CleanResponse{}, fmt.Errorf("the project %s does not exist, have you started a build yet?", harborProjectName)
		}
		return gen.CleanResponse{}, fmt.Errorf("error trying to get the repositories for project %s: %s", harborProjectName, err)
	}

	message := ""
	repositories := jsonBody.([]interface{})
	for _, repository := range repositories {
		// for some reason harbor returns the name bing <project>/<repository>, but then requires you to use only the last part for any queries
		log.Infof("Got repos: %v", repository)
		repoName := strings.SplitN(repository.(map[string]interface{})["name"].(string), "/", 2)[1]
		response, jsonBody, err := DoHarborRequest(api, "GET", fmt.Sprintf("/projects/%s/repositories/%s/artifacts", harborProjectName, repoName))
		if err != nil {
			return gen.CleanResponse{}, fmt.Errorf("error trying to get the artifacts for project %s: %s\n%d", harborProjectName, err, response.StatusCode)
		}

		artifacts := jsonBody.([]interface{})
		for _, artifact := range artifacts {
			_, _, err = DoHarborRequest(api, "DELETE", fmt.Sprintf("/projects/%s/repositories/%s/artifacts/%s", harborProjectName, repoName, artifact.(map[string]interface{})["digest"]))
			if err != nil {
				return gen.CleanResponse{}, fmt.Errorf("error trying to delete artifact %s from project %s: %s", artifact.(map[string]interface{})["digest"], harborProjectName, err)
			}
		}
		message += fmt.Sprintf("Deleted %d artifacts from harbor repository %s/%s\n", len(artifacts), harborProjectName, repoName)
	}
	if message == "" {
		message = "Nothing to clean up"
	}

	return gen.CleanResponse{Message: &message}, nil
}

func GetHarborQuota(api *BuildsApi, toolName string) (HarborQuotaResponse, error) {
	harborProjectName, err := ToolNameToHarborProjectName(toolName)
	if err != nil {
		return HarborQuotaResponse{}, err
	}

	url := fmt.Sprintf("%s/api/v2.0/projects/%s/summary", api.Config.HarborRepository, harborProjectName)

	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return HarborQuotaResponse{}, err
	}
	userAgent := "WMCS toolforge-builds-api Go-http-client"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	username := api.Config.HarborUsername
	password := api.Config.HarborPassword
	request.SetBasicAuth(username, password)

	response, err := api.Clients.Http.Do(request)
	if err != nil {
		return HarborQuotaResponse{}, HandleConnectionErrors(err)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}

	log.Debugf("Raw response body: %s", string(body))
	log.Debugf("Response status code: %d", response.StatusCode)
	log.Debugf("Response headers: %v", response.Header)

	if response.StatusCode != http.StatusOK {
		return HarborQuotaResponse{}, fmt.Errorf("failed to get harbor quota, status code: %d", response.StatusCode)
	}

	harborResponse := HarborQuota{}
	err = json.Unmarshal(body, &harborResponse)
	if err != nil {
		log.Printf("Error unmarshalling response: %v", err)
		return HarborQuotaResponse{}, err
	}

	storageHard := harborResponse.Quota.Hard["storage"]
	storageUsed := harborResponse.Quota.Used["storage"]

	var storageHardStr, storageAvailableStr, storageCapacityStr string
	if storageHard == -1 {
		storageHardStr = "Unlimited"
		storageAvailableStr = "Unlimited"
		storageCapacityStr = "0%"
	} else {
		storageAvailable := storageHard - storageUsed
		storageCapacity := (float64(storageUsed) / float64(storageHard)) * 100

		storageHardStr = FormatBytes(storageHard)
		storageAvailableStr = FormatBytes(storageAvailable)
		storageCapacityStr = fmt.Sprintf("%.0f%%", storageCapacity)
	}

	storageUsedStr := FormatBytes(storageUsed)

	res := HarborQuotaResponse{
		Categories: []Category{
			{
				Name: "Registry",
				Items: []Item{
					{Name: "Storage", Limit: storageHardStr, Used: storageUsedStr, Available: storageAvailableStr, Capacity: storageCapacityStr},
				},
			},
		},
	}

	return res, nil
}

func ValidateEnvvars(envvars map[string]string) error {
	for varname := range envvars {
		match := EnvvarsNameRegex.MatchString(varname)
		if !match {
			return fmt.Errorf("must match '^[A-z_][A-z_0-9]{2,}$', got '%s'", varname)
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
		message := fmt.Sprintf("Not valid environment variable name: %s", err)
		return http.StatusBadRequest, gen.BadRequest{Message: &message}
	}

	// TODO: Check quotas
	err = CreateHarborProjectForTool(api, toolName)
	if err != nil {
		message := fmt.Sprintf("Failed to create harbor project for tool %s: %s", toolName, err)
		return http.StatusServiceUnavailable, gen.InternalError{Message: &message}
	}
	cleanupErr := CleanupOldPipelineRuns(&api.Clients, api.Config.BuildNamespace, toolName, api.Config.OkToKeep, api.Config.FailedToKeep)
	for _, err := range cleanupErr {
		log.Warnf("Got error when cleaning up old pipeline runs: %s", err)
	}
	if imageName == "" {
		imageName = fmt.Sprintf("tool-%s", toolName)
	}
	var envvarsArray []string
	for varname, value := range envvars {
		envvarsArray = append(envvarsArray, fmt.Sprintf("%s=%s", varname, value))
	}
	log.Debugf("Starting a new build: ref=%s, imageName=%s, toolName=%s, harborRepository=%s, builder=%s", ref, imageName, toolName, api.Config.HarborRepository, api.Config.Builder)
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
						StringVal: fmt.Sprintf("%s/tool-%s/%s:latest", strings.Split(api.Config.HarborRepository, "//")[1], toolName, imageName),
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
				{
					Name:  "ENV_VARS",
					Value: v1beta1.ParamValue{ArrayVal: envvarsArray, Type: v1beta1.ParamTypeArray},
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
		Envvars:   &envvars,
	}
	return http.StatusOK, gen.NewBuild{
		Name:       &pipelineRun.Name,
		Parameters: &buildParams,
	}
}

func Logs(ctx echo.Context, api *BuildsApi, buildId string, toolName string, follow bool) (int, interface{}) {
	waitTimeout := 10 * time.Minute

	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}
	}

	listOptions := metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", buildId)}
	pipelineRuns, err := GetPipelineRuns(&api.Clients, api.Config.BuildNamespace, listOptions)
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

	err = StreamAfterPipelineRunStarted(ctx, &api.Clients, api.Config.BuildNamespace, follow, listOptions, waitTimeout)
	if err != nil {
		message := fmt.Sprintf("Error getting the logs for %s: %s", buildId, err)
		log.Errorf(message)
		// Note that when streaming, once the first line is sent, you can't really change the http error code,
		// so this internal server error is only effective if we did not yet send any data.
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	return http.StatusOK, nil
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

func Get(
	api *BuildsApi,
	buildId string,
	toolName string,
) (int, interface{}) {
	if err := ToolIsAllowedForBuild(toolName, buildId, api.Config.BuildIdPrefix); err != nil {
		message := fmt.Sprintf("%s", err)
		return http.StatusUnauthorized, gen.Unauthorized{Message: &message}
	}
	log.Debugf("Getting build: buildId=%s, namespace=%s, toolName=%s", buildId, api.Config.BuildNamespace, toolName)

	pipelineRuns, err := GetPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{FieldSelector: fmt.Sprintf("metadata.name=%s", buildId)})
	if err != nil {
		log.Warnf(
			"Got error when getting pipelinerun %s on namespace %s: %s", buildId, api.Config.BuildNamespace, err,
		)
		message := "Unable to get build! This might be a bug. Please contact a Toolforge admin."
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	if len(pipelineRuns) == 0 {
		message := fmt.Sprintf("Build with id %s not found.", buildId)
		return http.StatusNotFound, gen.NotFound{Message: &message}
	}

	// NOTE: we assume here the first pipelineRun from the search is what we are looking for.
	// In k8s/tekton two objects cannot share metadata.name in the same namespace anyway

	build := GetBuild(pipelineRuns[0])
	return http.StatusOK, build
}

func List(
	api *BuildsApi,
	toolName string,
) (int, interface{}) {
	log.Debugf("Listing builds: toolName=%s, namespace=%s", toolName, api.Config.BuildNamespace)
	pipelineRuns, err := GetPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		message := fmt.Sprintf("Got error when listing %s's pipelineruns on namespace %s: %s", toolName, api.Config.BuildNamespace, err)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}
	log.Debugf("Found %d pipelineruns for %s", len(pipelineRuns), toolName)

	builds := make([]gen.Build, len(pipelineRuns))
	for i, run := range pipelineRuns {
		builds[i] = *GetBuild(run)
	}
	return http.StatusOK, builds
}

func Latest(
	api *BuildsApi,
	toolName string,
) (int, interface{}) {
	log.Debugf("Getting latest build: namespace=%s, toolName=%s", api.Config.BuildNamespace, toolName)

	pipelineRuns, err := GetPipelineRuns(&api.Clients, api.Config.BuildNamespace, metav1.ListOptions{LabelSelector: fmt.Sprintf("user=%s", toolName)})
	if err != nil {
		log.Warnf(
			"Got error when getting latest pipelineruns on namespace %s: %s", api.Config.BuildNamespace, err,
		)
		message := "Unable to get build! This might be a bug. Please contact a Toolforge admin."
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	if len(pipelineRuns) == 0 {
		message := "No builds exist yet."
		return http.StatusNotFound, gen.NotFound{Message: &message}
	}

	// GetPipelineRuns returns a sorted array per creationTimestamp
	build := GetBuild(pipelineRuns[0])
	return http.StatusOK, build
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

	buildCondition := GetBuildConditionFromPipelineRun(pipelineRun)

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

	pipelineRun.Spec.Status = "PipelineRunCancelled"

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

	return http.StatusOK, gen.BuildId{Id: &buildId}
}

func Quota(api *BuildsApi, toolName string) (int, interface{}) {
	quota, err := GetHarborQuota(api, toolName)
	if err != nil {
		log.Error(err)
		message := fmt.Sprintf("Error getting quota from Harbor: %s", err)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	return http.StatusOK, quota
}

func Clean(api *BuildsApi, toolName string) (int, interface{}) {
	response, err := CleanHarbor(api, toolName)
	if err != nil {
		log.Error(err)
		message := fmt.Sprintf("Error cleaning up: %s", err)
		return http.StatusInternalServerError, gen.InternalError{Message: &message}
	}

	return http.StatusOK, response
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
