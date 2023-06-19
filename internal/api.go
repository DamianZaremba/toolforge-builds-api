package internal

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
)

type UserContextKey string

type UserContext struct {
	echo.Context
	User string
}

type Config struct {
	HarborRepository string
	Builder          string
	OkToKeep         int
	FailedToKeep     int
	BuildNamespace   string
	BuildIdPrefix    string
}

type BuildsApi struct {
	Clients Clients
	Config  Config
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

func (api BuildsApi) Logs(ctx echo.Context, buildId string) error {
	toolName := getToolFromContext(ctx)

	code, response, err := Logs(&api, buildId, toolName)
	if err != nil {
		return err
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

	code, response, err := Start(
		&api,
		safeDeref[string](newBuildParameters.SourceUrl),
		safeDeref[string](newBuildParameters.Ref),
		toolName,
	)
	if err != nil {
		return err
	}
	return ctx.JSON(code, response)
}

func (api BuildsApi) Delete(ctx echo.Context, id string) error {
	toolName := getToolFromContext(ctx)

	code, response, err := Delete(&api, id, toolName)
	if err != nil {
		return ctx.JSON(code, response)
	}
	return err
}

func (api BuildsApi) Healthcheck(ctx echo.Context) error {
	code, response, err := Healthcheck(&api)
	if err != nil {
		return err
	}
	return ctx.JSON(code, response)
}
