package ingress

import (
	"flag"
	"fmt"
	"ingress-controller/kube"
	"net/http"
)

const AnnotationKubernetesIngressClass = "kubernetes.io/ingress.class"

var ingressClassName = flag.String("ingress-class", "", "")

const (
	PathTypePrefix                 = "Prefix"
	PathTypeExact                  = "Exact"
	PathTypeImplementationSpecific = "ImplementationSpecific"
)

type TLS struct {
	SecretName string   `json:"secretName"`
	Host       []string `json:"hosts"`
}

type Ingress struct {
	Metadata *kube.Metadata `json:"metadata"`
	Spec     struct {
		Rules []*Rule `json:"rules"`
		TLS   []*TLS  `json:"tls"`
	} `json:"spec"`
}

type Rule struct {
	Host string `json:"host"`
	Http struct {
		Paths []*Path `json:"paths"`
	} `json:"http"`
}

type Path struct {
	Path     string `json:"path"`
	PathType string `json:"pathType"`
	Backend  struct {
		Service Service `json:"service"`
	} `json:"backend"`
}

type Service struct {
	Name string `json:"name"`
	Port struct {
		Number int `json:"number"`
	} `json:"port"`
}

func (i *Ingress) Name() string {
	return fmt.Sprintf("%s/%s", i.Metadata.Namespace, i.Metadata.Name)
}

func WatchFunc(r *http.Request) {
	r.URL.Path = "/apis/networking.k8s.io/v1/watch/ingresses"
}

func ListFunc(r *http.Request) {
	r.URL.Path = "/apis/networking.k8s.io/v1/ingresses"
}

func FilterIngress(is *Ingress) bool {
	if *ingressClassName == "" {
		return true
	}

	if annos := is.Metadata.Annotations; annos != nil {
		if v, ok := annos[AnnotationKubernetesIngressClass]; ok {
			return v == *ingressClassName
		}
	}

	return false
}
