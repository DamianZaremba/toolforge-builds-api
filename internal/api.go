package internal

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
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

func (api BuildsApi) Logs(ctx echo.Context, buildId string) error {
	toolName := ctx.Get("user").(string)
	namespace := fmt.Sprintf("tool-%s", toolName)

	code, response, err := Logs(&api, buildId, namespace, toolName)
	if err != nil {
		return err
	}
	return ctx.JSON(code, response)
}

func (api BuildsApi) Start(ctx echo.Context) error {
	uCtx := ctx.(UserContext)
	log.Infof("ctx: %v", uCtx)
	toolName := uCtx.User
	namespace := fmt.Sprintf("tool-%s", toolName)
	var newBuildParameters gen.NewBuildParameters
	err := ctx.Bind(&newBuildParameters)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, fmt.Sprintf("Bad parameters passed: %s", err))
	}

	code, response, err := Start(&api, *newBuildParameters.SourceUrl, *newBuildParameters.SourceUrl, namespace, toolName)
	if err != nil {
		return err
	}
	return ctx.JSON(code, response)
}

func (api BuildsApi) Delete(ctx echo.Context, id string) error {
	uCtx := ctx.(*UserContext)
	toolName := uCtx.User
	namespace := fmt.Sprintf("tool-%s", toolName)

	code, response, err := Delete(&api, namespace, id, toolName)
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
