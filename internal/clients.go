package internal

import (
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"k8s.io/client-go/kubernetes"
)

type Clients struct {
	Tekton versioned.Interface
	K8s    kubernetes.Interface
}
