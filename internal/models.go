package internal

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
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

type HarborQuota struct {
	Quota struct {
		Hard map[string]int64 `json:"hard"`
		Used map[string]int64 `json:"used"`
	} `json:"quota"`
}

type HarborQuotaResponse struct {
	Categories []Category `json:"categories"`
}

type Category struct {
	Name  string `json:"name"`
	Items []Item `json:"items"`
}

type Item struct {
	Name      string `json:"name"`
	Limit     string `json:"limit"`
	Used      string `json:"used"`
	Available string `json:"available,omitempty"`
	Capacity  string `json:"capacity,omitempty"`
}
