package main

import (
	"context"
	"fmt"
	"os"

	"github.com/deepmap/oapi-codegen/pkg/middleware"
	"github.com/getkin/kin-openapi/openapi3filter"
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
	viper.SetDefault("builder", "tools-harbor.wmcloud.org/toolforge/heroku-builder-classic:22")
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

	var k8sConfig *rest.Config
	var err error
	if viper.GetBool("out_of_k8s_run") {
		// use the current context in kubeconfig
		kubeconfig := viper.GetString("kubeconfig")
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	} else {
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			log.Fatalln(err.Error())
		}
	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		log.Fatalln(err.Error())
	}
	// creates the tekton clientset
	tektonClientset, err := versioned.NewForConfig(k8sConfig)
	if err != nil {
		log.Fatalln(err.Error())
	}

	buildsApi := internal.BuildsApi{
		Clients: internal.Clients{
			Tekton: tektonClientset,
			K8s:    clientset,
		},
		Config: internal.Config{
			HarborRepository: viper.GetString("harbor_repository"),
			Builder:          viper.GetString("builder"),
			OkToKeep:         viper.GetInt("ok_builds_to_keep"),
			FailedToKeep:     viper.GetInt("failed_builds_to_keep"),
			BuildNamespace:   viper.GetString("build_namespace"),
			BuildIdPrefix:    viper.GetString("build_id_prefix"),
		},
	}
	log.Infof("Using config: %v", buildsApi.Config)

	swagger, err := gen.GetSwagger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading swagger spec\n: %s", err)
		os.Exit(1)
	}

	//strictHandler := gen.NewStrictHandler(buildsApi, nil)
	e := echo.New()
	e.HideBanner = true
	// Log all requests
	e.Use(echomiddleware.Logger())

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
	e.Use(authValidator)
	// Set the user on the context for the requests if there's any, "" if there's none
	// This is needed as the above authenticator does not have access to call next with a new context
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
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
	e.Use(echomiddleware.Recover())

	gen.RegisterHandlersWithBaseURL(e, buildsApi, "/v1")
	log.Info("Registered routes:")
	for _, route := range e.Routes() {
		log.Infof("%v", *route)
	}

	e.Logger.Fatal(e.Start(fmt.Sprintf("0.0.0.0:%d", port)))

}
