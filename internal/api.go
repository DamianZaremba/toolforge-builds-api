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
	"fmt"
	"net/http"

	"github.com/goharbor/go-client/pkg/harbor"
	harborModels "github.com/goharbor/go-client/pkg/sdk/v2.0/models"
	"github.com/labstack/echo/v4"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	"k8s.io/client-go/kubernetes"
)

type UserContextKey string

type UserContext struct {
	echo.Context
	User string
}

type Config struct {
	HarborRepository  string
	HarborUsername    string
	HarborPassword    string
	Builder           string
	Runner            string
	OkToKeep          int
	FailedToKeep      int
	BuildNamespace    string
	BuildIdPrefix     string
	MaxParallelBuilds int
}

type Clients struct {
	Tekton versioned.Interface
	K8s    kubernetes.Interface
	Http   *http.Client
	Harbor *harbor.ClientSet
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
