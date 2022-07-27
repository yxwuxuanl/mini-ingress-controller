package ingress

import "ingress-controller/kube"

const AnnotationKubernetesIngressClass = "kubernetes.io/ingress.class"

const (
	PathTypePrefix                 = "Prefix"
	PathTypeExact                  = "Exact"
	PathTypeImplementationSpecific = "ImplementationSpecific"
)

type Ingress struct {
	Metadata *kube.Metadata `json:"metadata"`
	Spec     struct {
		Rules []*Rule `json:"rules"`
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
