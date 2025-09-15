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
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/goharbor/go-client/pkg/harbor"
	harborModels "github.com/goharbor/go-client/pkg/sdk/v2.0/models"
	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
	tektonPipelineV1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type UserContextKey string

type UserContext struct {
	echo.Context
	User string
}

type Config struct {
	HarborRepository string
	HarborUsername   string
	HarborPassword   string
	Builder          string
	Runner           string
	// the Latest* fields are the ones used when useLatestBuilder is passed
	LatestBuilder     string
	LatestRunner      string
	OkToKeep          int
	FailedToKeep      int
	BuildNamespace    string
	BuildIdPrefix     string
	MaxParallelBuilds int
}

type Clients struct {
	Tekton    versioned.Interface
	K8s       kubernetes.Interface
	K8sCustom dynamic.Interface
	Http      *http.Client
	Harbor    *harbor.ClientSet
}

type BuildsApi struct {
	Clients        Clients
	Config         Config
	MetricsHandler echo.HandlerFunc
}

type HarborError interface {
	GetPayload() *harborModels.Errors
}

// Extract the tool name from the context, currently is the same user that authenticated to the api
func getToolFromContext(ctx echo.Context) string {
	uCtx := ctx.(UserContext)
	return uCtx.User
}

func enforceLoggedIn(ctx echo.Context) error {
	toolnameFromContext := getToolFromContext(ctx)
	if toolnameFromContext == "" {
		return fmt.Errorf("this endpoint needs authentication")
	}
	return nil
}

// As we deal with pointers a lot when binding request parameters, this function helps dereferencing without
// having to check if the pointers are null every time
func safeDeref[T any](pointer *T) T {
	if pointer == nil {
		var value T
		return value
	}
	return *pointer
}

func (api BuildsApi) Logs(ctx echo.Context, toolnameFromRequest string, buildId string, params gen.LogsParams) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	if *params.Follow {
		// Disable buffering on nginx to be able to stream replies
		// see https://www.nginx.com/resources/wiki/start/topics/examples/x-accel/#x-accel-buffering
		ctx.Response().Header().Set("X-Accel-Buffering", "no")
	}
	code, response := Logs(ctx, &api, buildId, toolnameFromContext, *params.Follow)
	if response == nil {
		return nil
	}
	return ctx.JSON(code, response)
}

func (api BuildsApi) Start(ctx echo.Context, toolnameFromRequest string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	var buildParameters gen.BuildParameters
	err = (&echo.DefaultBinder{}).BindBody(ctx, &buildParameters)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, fmt.Sprintf("Bad parameters passed: %s", err))
	}

	code, response := Start(
		&api,
		safeDeref[string](&buildParameters.SourceUrl),
		safeDeref[string](buildParameters.Ref),
		safeDeref[string](buildParameters.ImageName),
		toolnameFromContext,
		safeDeref[map[string]string](buildParameters.Envvars),
		safeDeref[bool](buildParameters.UseLatestVersions),
	)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Delete(ctx echo.Context, toolnameFromRequest string, id string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := Delete(&api, id, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Cancel(ctx echo.Context, toolnameFromRequest string, id string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := Cancel(&api, id, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Healthcheck(ctx echo.Context) error {
	code, response := Healthcheck(&api)
	return ctx.JSON(code, response)
}

func (api BuildsApi) List(ctx echo.Context, toolnameFromRequest string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := List(&api, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Get(ctx echo.Context, toolnameFromRequest string, id string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := Get(&api, id, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Latest(ctx echo.Context, toolnameFromRequest string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := Latest(&api, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Quota(ctx echo.Context, toolnameFromRequest string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := Quota(&api, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Clean(ctx echo.Context, toolnameFromRequest string) error {
	err := enforceLoggedIn(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	toolnameFromContext := getToolFromContext(ctx)
	code, response := Clean(&api, toolnameFromContext)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Openapi(ctx echo.Context) error {
	swagger, err := gen.GetSwagger()
	if err != nil {
		return fmt.Errorf("error loading swagger spec\n: %s", err)
	}
	return ctx.JSON(http.StatusOK, swagger)
}

func (api BuildsApi) Metrics(ctx echo.Context) error {
	return api.MetricsHandler(ctx)
}

func submitScheduledBuildsToTekton(clients *Clients, config *Config) error {
	log.Info("submitting scheduled builds to Tekton...")
	scheduledPipelineRuns, err := GetScheduledBuilds(clients, config.BuildNamespace, metav1.ListOptions{})
	if err != nil {
		log.Errorf("Failed to get scheduled builds: %v", err)
		return err
	}
	if len(scheduledPipelineRuns) == 0 {
		log.Info("No scheduled builds found. returning...")
		return nil
	}
	userscheduledPipelineRuns := make(map[string][]tektonPipelineV1.PipelineRun, len(scheduledPipelineRuns))
	for _, run := range scheduledPipelineRuns {
		user := run.Labels["user"]
		userscheduledPipelineRuns[user] = append(userscheduledPipelineRuns[user], run)
	}
	users := make([]string, 0, len(userscheduledPipelineRuns))
	for user := range userscheduledPipelineRuns {
		users = append(users, user)
	}
	usersSelector := metav1.ListOptions{LabelSelector: fmt.Sprintf("user in (%s)", strings.Join(users, ","))}
	tektonPipelineRuns, err := GetTektonPipelineRuns(clients, config.BuildNamespace, usersSelector)
	if err != nil {
		return fmt.Errorf("failed to get tekton pipeline runs for users (%s): %v", strings.Join(users, ","), err)
	}

	runningPipelineRuns := FilterPipelineRunsByStatus(tektonPipelineRuns, []gen.BuildStatus{gen.BUILDRUNNING, gen.BUILDPENDING})
	log.Infof("Got %d running pipeline runs for users (%s)", len(runningPipelineRuns), strings.Join(users, ","))
	userRunningPipelineRunsCount := make(map[string]int)
	for _, run := range runningPipelineRuns {
		userRunningPipelineRunsCount[run.Labels["user"]]++
	}
	for user, scheduledPipelineRuns := range userscheduledPipelineRuns {

		availableSlots := config.MaxParallelBuilds - userRunningPipelineRunsCount[user]
		if availableSlots <= 0 {
			log.Infof("User %s has reached max parallel builds (%d), skipping...", user, config.MaxParallelBuilds)
			continue
		}
		submittedCount := 0
		for _, scheduledRun := range scheduledPipelineRuns {
			if submittedCount >= availableSlots {
				break
			}
			_, err := clients.Tekton.TektonV1().PipelineRuns(config.BuildNamespace).Create(context.TODO(), &scheduledRun, metav1.CreateOptions{})
			if err != nil {
				log.Errorf("failed to submit scheduled build %s for user %s to Tekton: %v", scheduledRun.Name, user, err)
				continue
			}
			err = clients.K8sCustom.Resource(ScheduledBuildResource).Namespace(config.BuildNamespace).Delete(
				context.TODO(),
				scheduledRun.Name,
				metav1.DeleteOptions{},
			)
			if err != nil {
				log.Errorf("Failed to delete scheduled build %s: %v", scheduledRun.Name, err)
				// we can ignore this error. Sure it will trigger re-run of this pipelinerun on tekton after a while,
				// the k8s request that failed is most likely transient and probably won't happen again the next time the pipeline is run
			}
			submittedCount++
			log.Infof("Submitted scheduled build %s for user %s (%d/%d slots used)",
				scheduledRun.Name, user, submittedCount, availableSlots)
		}
		log.Infof("Processed %d scheduled builds for user %s, submitted %d",
			len(scheduledPipelineRuns), user, submittedCount)
	}
	log.Info("Completed pipeline run submission process")
	return nil
}

// StartPipelinerunSubmissionToTekton calls schedulePipelineRuns repeatedly after some time interval.
func StartScheduledBuildsSubmissionToTekton(ctx context.Context, clients *Clients, config *Config) {
	log.Info("Starting scheduled builds submission loop...")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("Stopping scheduled builds submission loop...")
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("recovered from a panic while submitting scheduled builds to tekton: %v", r)
					}
				}()
				err := submitScheduledBuildsToTekton(clients, config)
				if err != nil {
					log.Errorf("Error while submitting scheduled builds to tekton: %v", err)
				}
			}()
		}
	}
}
