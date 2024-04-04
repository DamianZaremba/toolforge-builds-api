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
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/deepmap/oapi-codegen/pkg/middleware"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/goharbor/go-client/pkg/harbor"
	prom "github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	gen "gitlab.wikimedia.org/repos/toolforge/builds-api/gen"
	internal "gitlab.wikimedia.org/repos/toolforge/builds-api/internal"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})

	viper.AutomaticEnv()
	viper.AllowEmptyEnv(true)
	viper.SetDefault("debug", false)
	viper.SetDefault("port", 8000)
	// note that the builder is the full ref for the image
	viper.SetDefault("builder", "tools-harbor.wmcloud.org/toolforge/heroku-builder:22")
	viper.SetDefault("ok_builds_to_keep", 1)
	viper.SetDefault("failed_builds_to_keep", 2)
	viper.SetDefault("build_namespace", internal.BuildNamespace)
	viper.SetDefault("build_id_prefix", internal.BuildIdPrefix)
	viper.SetDefault("out_of_k8s_run", false)
	viper.SetDefault("kubeconfig", fmt.Sprintf("%s/.kube/config", os.Getenv("HOME")))
	if viper.GetBool("debug") {
		log.SetLevel(log.DebugLevel)
		log.Info("Starting in DEBUG mode")
	}

	port := viper.GetInt("port")

	harborRepository := viper.GetString("HARBOR_REPOSITORY")
	if harborRepository == "" {
		log.Fatalf("No HARBOR_REPOSITORY set, one is needed.")
	}
	harborUsername := viper.GetString("HARBOR_USERNAME")
	if harborUsername == "" {
		log.Fatalf("No HARBOR_USERNAME set, one is needed.")
	}
	harborPassword := viper.GetString("HARBOR_PASSWORD")
	if harborPassword == "" {
		log.Fatalf("No HARBOR_PASSWORD set, one is needed.")
	}

	kubeconfig := viper.GetString("kubeconfig")
	outOfK8sRun := viper.GetBool("out_of_k8s_run")

	internalConfig := &internal.Config{
		HarborRepository: harborRepository,
		HarborUsername:   harborUsername,
		HarborPassword:   harborPassword,
		Builder:          viper.GetString("builder"),
		OkToKeep:         viper.GetInt("ok_builds_to_keep"),
		FailedToKeep:     viper.GetInt("failed_builds_to_keep"),
		BuildNamespace:   viper.GetString("build_namespace"),
		BuildIdPrefix:    viper.GetString("build_id_prefix"),
	}

	clients, err := getApiClients(outOfK8sRun, kubeconfig, internalConfig)
	if err != nil {
		log.Fatalln(err.Error())
	}
	buildsApi := internal.BuildsApi{
		Clients:        *clients,
		Config:         *internalConfig,
		MetricsHandler: prom.NewHandler(),
	}
	log.Infof("Using config: %v", buildsApi.Config)

	//strictHandler := gen.NewStrictHandler(buildsApi, nil)
	e := echo.New()
	e.HideBanner = true

	err = addMiddleware(e)
	if err != nil {
		log.Fatalf("Error setting up middlewares: %s", err)
	}

	gen.RegisterHandlersWithBaseURL(e, buildsApi, "")
	log.Info("Registered routes:")
	for _, route := range e.Routes() {
		log.Infof("%v", *route)
	}

	e.Logger.Fatal(e.Start(fmt.Sprintf("0.0.0.0:%d", port)))

}

func getApiClients(outOfK8sRun bool, kubeconfig string, internalConfig *internal.Config) (*internal.Clients, error) {
	var k8sConfig *rest.Config
	var err error
	if outOfK8sRun {
		// use the current context in kubeconfig
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	} else {
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, err
	}
	// creates the tekton clientset
	tektonClientset, err := versioned.NewForConfig(k8sConfig)
	if err != nil {
		return nil, err
	}
	// create the http client
	http := &http.Client{Timeout: 8 * time.Second}

	harborClientSetConfig := &harbor.ClientSetConfig{
		URL:      internalConfig.HarborRepository,
		Password: internalConfig.HarborPassword,
		Username: internalConfig.HarborUsername,
		Insecure: false,
	}

	harborClientset, err := harbor.NewClientSet(harborClientSetConfig)
	if err != nil {
		return nil, err
	}

	return &internal.Clients{
		Tekton: tektonClientset,
		K8s:    clientset,
		Http:   http,
		Harbor: harborClientset,
	}, nil
}

func addMiddleware(router *echo.Echo) error {
	// Log all requests
	router.Use(echomiddleware.Logger())

	swagger, err := gen.GetSwagger()
	if err != nil {
		return fmt.Errorf("error loading swagger spec\n: %s", err)
	}

	// authentication
	authValidator := middleware.OapiRequestValidatorWithOptions(
		swagger,
		&middleware.Options{
			Options: openapi3filter.Options{
				AuthenticationFunc: func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
					log.Debugf("Authenticating for %s", input.RequestValidationInput.Request.URL.RequestURI())
					_, err := internal.GetUserFromRequest(input.RequestValidationInput.Request)
					if err != nil {
						return err
					}
					return nil
				},
			},
		},
	)
	router.Use(authValidator)
	// Set the user on the context for the requests if there's any, "" if there's none
	// This is needed as the above authenticator does not have access to call next with a new context
	router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			uCtx := internal.UserContext{
				Context: c,
				User:    "",
			}
			user, err := internal.GetUserFromRequest(c.Request())
			if err == nil {
				uCtx.User = user
			}

			return next(uCtx)
		}
	})

	// Recover from panics and give control to the HTTPError handler
	// so we reply http
	router.Use(echomiddleware.Recover())

	router.Use(prom.NewMiddleware("builds_api"))
	return nil
}
