// This file is safe to edit. Once it exists it will not be overwritten
// MANUALLY CHANGED

package restapi

import (
	"crypto/tls"
	"net/http"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/runtime"
	"github.com/go-openapi/runtime/middleware"

	interpose "github.com/carbocation/interpose/middleware"
	"gitlab.wikimedia.org/repos/toolforge/toolforge-builds-api/gen/models"
	"gitlab.wikimedia.org/repos/toolforge/toolforge-builds-api/gen/restapi/operations"
	"gitlab.wikimedia.org/repos/toolforge/toolforge-builds-api/internal"
)

//go:generate swagger generate server --target ../../gen --name ToolforgeBuilds --spec ../../swagger/swagger_v1.yaml --principal models.Principal --exclude-main

func configureFlags(api *operations.ToolforgeBuildsAPI) {
	// api.CommandLineOptionsGroups = []swag.CommandLineOptionsGroup{ ... }
}

func configureAPI(api *operations.ToolforgeBuildsAPI) http.Handler {
	// configure the api here
	api.ServeError = errors.ServeError

	// Set your custom logger if needed. Default one is log.Printf
	// Expected interface func(string, ...interface{})
	//
	// Example:
	// api.Logger = log.Printf

	api.UseSwaggerUI()
	// To continue using redoc as your UI, uncomment the following line
	// api.UseRedoc()

	api.JSONConsumer = runtime.JSONConsumer()

	api.JSONProducer = runtime.JSONProducer()

	// Applies when the "ssl-client-subject-dn" header is set
	if api.KeyAuth == nil {
		api.KeyAuth = func(token string) (*models.Principal, error) {
			return nil, errors.NotImplemented("api key auth (key) ssl-client-subject-dn from header param [ssl-client-subject-dn] has not yet been implemented")
		}
	}

	// Set your custom authorizer if needed. Default one is security.Authorized()
	// Expected interface runtime.Authorizer
	//
	// Example:
	// api.APIAuthorizer = security.Authorized()

	if api.HealthcheckHandler == nil {
		api.HealthcheckHandler = operations.HealthcheckHandlerFunc(func(params operations.HealthcheckParams) middleware.Responder {
			return middleware.NotImplemented("operation operations.Healthcheck has not yet been implemented")
		})
	}
	if api.LogsHandler == nil {
		api.LogsHandler = operations.LogsHandlerFunc(func(params operations.LogsParams, principal *models.Principal) middleware.Responder {
			return middleware.NotImplemented("operation operations.List has not yet been implemented")
		})
	}

	api.PreServerShutdown = func() {}

	api.ServerShutdown = func() {}

	return setupGlobalMiddleware(api.Serve(setupMiddlewares))
}

// The TLS configuration before HTTPS server starts.
func configureTLS(tlsConfig *tls.Config) {
	// Make all necessary changes to the TLS configuration here.
}

// As soon as server is initialized but not run yet, this function will be called.
// If you need to modify a config, store server instance to stop it individually later, this is the place.
// This function can be called multiple times, depending on the number of serving schemes.
// scheme value will be set accordingly: "http", "https" or "unix".
func configureServer(s *http.Server, scheme, addr string) {
}

// The middleware configuration is for the handler executors. These do not apply to the swagger.json document.
// The middleware executes after routing but before authentication, binding and validation.
func setupMiddlewares(handler http.Handler) http.Handler {
	return handler
}

// The middleware configuration happens before anything, this middleware also applies to serving the swagger.json document.
// So this is a good place to plug in a panic handling middleware, logging and metrics.
func setupGlobalMiddleware(handler http.Handler) http.Handler {
	logViaLogrus := interpose.NegroniLogrus()

	return internal.RecoverPanic(logViaLogrus(handler))
}
