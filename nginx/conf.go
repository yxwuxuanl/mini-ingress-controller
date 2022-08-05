package nginx

import (
	"ingress-controller/kube/ingress"
	"strings"
)

const proxyPassParams = `
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Port $server_port;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_buffering on;
`

type Path struct {
	Path     string
	PathType string
	Regex    bool
}

func (p Path) String() string {
	if p.Regex {
		return "~* " + p.Path
	}

	if p.PathType == ingress.PathTypeExact {
		return "= " + p.Path
	}

	return p.Path
}

type Directive []string

func (d Directive) String() string {
	return strings.Join(d, " ")
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
	Directives       []Directive
}

type ReturnConf struct {
	Code int
	Text string
}

type TLSConf struct {
	Cert string
	Key  string
}

type Server struct {
	ServerName string
	Locations  map[string]*Location
	SSL        *TLSConf
}

type Main struct {
	WorkerProcesses   int
	WorkerConnections int
	LogLevel          string
	User              string
	PidFile           string
}

type Http struct {
	LogFormat  string
	AccessLog  string
	Listen     int
	TLSListen  int
	Servers    map[string]*Server
	SSLServers map[string]*Server
}

func (h *Http) AllServers() []*Server {
	var ss []*Server

	for _, server := range h.Servers {
		ss = append(ss, server)
	}

	for _, server := range h.SSLServers {
		ss = append(ss, server)
	}

	return ss
}
