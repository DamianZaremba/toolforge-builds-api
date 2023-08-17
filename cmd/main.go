package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

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
	clients, err := getApiClients(outOfK8sRun, kubeconfig)
	if err != nil {
		log.Fatalln(err.Error())
	}
	buildsApi := internal.BuildsApi{
		Clients: *clients,
		Config: internal.Config{
			HarborRepository: viper.GetString("harbor_repository"),
			HarborUsername:   viper.GetString("harbor_username"),
			HarborPassword:   viper.GetString("harbor_password"),
			Builder:          viper.GetString("builder"),
			OkToKeep:         viper.GetInt("ok_builds_to_keep"),
			FailedToKeep:     viper.GetInt("failed_builds_to_keep"),
			BuildNamespace:   viper.GetString("build_namespace"),
			BuildIdPrefix:    viper.GetString("build_id_prefix"),
		},
	}
	log.Infof("Using config: %v", buildsApi.Config)

	//strictHandler := gen.NewStrictHandler(buildsApi, nil)
	e := echo.New()
	e.HideBanner = true

	err = addMiddleware(e)
	if err != nil {
		log.Fatalf("Error setting up middlewares: %s", err)
	}

	gen.RegisterHandlersWithBaseURL(e, buildsApi, "/v1")
	log.Info("Registered routes:")
	for _, route := range e.Routes() {
		log.Infof("%v", *route)
	}

	e.Logger.Fatal(e.Start(fmt.Sprintf("0.0.0.0:%d", port)))

}

func getApiClients(outOfK8sRun bool, kubeconfig string) (*internal.Clients, error) {
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
	http := &http.Client{Timeout: 60 * time.Second}

	return &internal.Clients{
		Tekton: tektonClientset,
		K8s:    clientset,
		Http:   http,
	}, nil
}

func addMiddleware(router *echo.Echo) error {
	// Log all requests
	router.Use(echomiddleware.Logger())

	swagger, err := gen.GetSwagger()
	if err != nil {
		return fmt.Errorf("Error loading swagger spec\n: %s", err)
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
	return nil
}
