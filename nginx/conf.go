package nginx

import (
	"ingress-controller/ingress"
	"net/url"
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

type Location struct {
	Path
	Upstream   *url.URL
	IngressRef string
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
	Prefix            string
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
