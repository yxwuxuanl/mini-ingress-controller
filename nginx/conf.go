package nginx

import (
	"fmt"
	"ingress-controller/kube/ingress"
	"strings"
)

type Path struct {
	Path     string
	PathType string
}

func (p Path) String() string {
	if p.PathType == ingress.PathTypePrefix {
		return p.Path
	}

	return p.Path + " (Exact)"
}

type Directive struct {
	Directive string
	Args      []string
}

func (d Directive) String() string {
	return fmt.Sprintf("%s %s", d.Directive, strings.Join(d.Args, " "))
}

type ProxyPassConf struct {
	Upstream string
}

type BasicAuthConf struct {
	Realm    string
	UserFile string
}

type Location struct {
	Path
	ProxyPass        *ProxyPassConf
	BasicAuth        *BasicAuthConf
	Return           *ReturnConf
	DisableAccessLog bool
	IngressRef       string
}

type ReturnConf struct {
	StatusCode int
	Status     string
}

type Server struct {
	ServerName string
	Locations  map[Path]*Location
}

type Main struct {
	WorkerProcesses   int
	WorkerConnections int
	User              string
	PidFile           string
}

type Http struct {
	LogFormat string
	AccessLog string
	Listen    int
	Servers   map[string]*Server
}

type HttpTplData struct {
	*Http
	NgxPrefix string
}
