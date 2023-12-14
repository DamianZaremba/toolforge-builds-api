package internal

import (
	"fmt"
	"net/http"

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
	HarborRepository string
	HarborUsername   string
	HarborPassword   string
	Builder          string
	OkToKeep         int
	FailedToKeep     int
	BuildNamespace   string
	BuildIdPrefix    string
}

type Clients struct {
	Tekton versioned.Interface
	K8s    kubernetes.Interface
	Http   *http.Client
}

type BuildsApi struct {
	Clients        Clients
	Config         Config
	MetricsHandler echo.HandlerFunc
}

// Extract the tool name from the context, currently is the same user that authenticated to the api
func getToolFromContext(ctx echo.Context) string {
	uCtx := ctx.(UserContext)
	return uCtx.User
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

func (api BuildsApi) Logs(ctx echo.Context, buildId string, params gen.LogsParams) error {
	toolName := getToolFromContext(ctx)
	if *params.Follow {
		// Disable buffering on nginx to be able to stream replies
		// see https://www.nginx.com/resources/wiki/start/topics/examples/x-accel/#x-accel-buffering
		ctx.Response().Header().Set("X-Accel-Buffering", "no")
	}
	code, response := Logs(ctx, &api, buildId, toolName, *params.Follow)
	if response == nil {
		return nil
	}
	return ctx.JSON(code, response)
}

func (api BuildsApi) Start(ctx echo.Context) error {
	toolName := getToolFromContext(ctx)
	var newBuildParameters gen.NewBuildParameters
	err := (&echo.DefaultBinder{}).BindBody(ctx, &newBuildParameters)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, fmt.Sprintf("Bad parameters passed: %s", err))
	}

	code, response := Start(
		&api,
		safeDeref[string](newBuildParameters.SourceUrl),
		safeDeref[string](newBuildParameters.Ref),
		safeDeref[string](newBuildParameters.ImageName),
		toolName,
		safeDeref[map[string]string](newBuildParameters.Envvars),
	)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Delete(ctx echo.Context, id string) error {
	toolName := getToolFromContext(ctx)

	code, response := Delete(&api, id, toolName)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Cancel(ctx echo.Context, id string) error {
	toolName := getToolFromContext(ctx)

	code, response := Cancel(&api, id, toolName)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Healthcheck(ctx echo.Context) error {
	code, response := Healthcheck(&api)
	return ctx.JSON(code, response)
}

func (api BuildsApi) List(ctx echo.Context) error {
	toolName := getToolFromContext(ctx)

	code, response := List(&api, toolName)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Get(ctx echo.Context, id string) error {
	toolName := getToolFromContext(ctx)

	code, response := Get(&api, id, toolName)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Latest(ctx echo.Context) error {
	toolName := getToolFromContext(ctx)

	code, response := Latest(&api, toolName)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Quota(ctx echo.Context) error {
	toolName := getToolFromContext(ctx)

	code, response := Quota(&api, toolName)
	return ctx.JSON(code, response)
}

func (api BuildsApi) Clean(ctx echo.Context) error {
	toolName := getToolFromContext(ctx)

	code, response := Clean(&api, toolName)
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
