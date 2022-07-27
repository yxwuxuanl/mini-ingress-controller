package nginx

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"syscall"
	"text/template"
	"time"
)

const MainLogFormat = `'$remote_addr - $remote_user [$time_local] "$request" '
'$status $body_bytes_sent "$http_referer" '
'"$http_user_agent" "$http_x_forwarded_for"'`

//go:embed templates/nginx.gotpl
var _nginxTpl string

//go:embed templates/http.gotpl
var _httpTpl string

var (
	nginxTpl *template.Template
	httpTpl  *template.Template
)

func init() {
	var err error

	funcMap := template.FuncMap{
		"now": func() string {
			return time.Now().Format(time.RFC3339)
		},
	}

	if nginxTpl, err = template.New("nginx.nginx").Funcs(funcMap).Parse(_nginxTpl); err != nil {
		panic(err)
	}

	if httpTpl, err = template.New("nginx.http").Funcs(funcMap).Parse(_httpTpl); err != nil {
		panic(err)
	}
}

type Nginx struct {
	mainConf *Config
	httpConf *HttpConfig
	cmd      *exec.Cmd
	stopCh   chan struct{}
}

func (ngx *Nginx) AddLocation(host string, loc *LocationConfig) error {
	if host == "" {
		host = "_"

		if loc.Path.Path == "/" {
			return errors.New("definition is not allowed")
		}
	}

	server, ok := ngx.httpConf.Servers[host]

	if !ok {
		server = &ServerConfig{
			ServerName: host,
			Locations:  map[Path]*LocationConfig{},
		}

		ngx.httpConf.Servers[host] = server
	}

	locations := server.Locations

	if _, ok := locations[loc.Path]; ok {
		return fmt.Errorf("nginx: duplicated location %s", loc.Path)
	}

	locations[loc.Path] = loc
	return nil
}

func (ngx *Nginx) DeleteLocation(host string, loc *LocationConfig) {
	if host == "" {
		host = "_"
	}

	server, ok := ngx.httpConf.Servers[host]

	if !ok {
		return
	}

	var locNum int

	for p, _loc := range server.Locations {
		if p.String() == loc.String() && _loc.IngressRef == loc.IngressRef {
			delete(server.Locations, p)
		} else {
			locNum++
		}
	}

	if locNum == 0 && host != "_" {
		delete(ngx.httpConf.Servers, host)
	}
}

func (ngx *Nginx) BuildHttpConfig() error {
	var buf bytes.Buffer

	if err := httpTpl.Execute(&buf, ngx.httpConf); err != nil {
		return err
	}

	return ioutil.WriteFile(ngx.mainConf.Prefix+"/http.conf", buf.Bytes(), 0777)
}

func (ngx *Nginx) BuildMainConfig() error {
	var buf bytes.Buffer

	if err := nginxTpl.Execute(&buf, ngx.mainConf); err != nil {
		return err
	}

	return ioutil.WriteFile(ngx.mainConf.Prefix+"/nginx.conf", buf.Bytes(), 0777)
}

func (ngx *Nginx) Reload() {
	if os.Getenv("NO_NGINX") == "1" {
		return
	}

	if err := ngx.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		log.Printf("nginx: SIGHUP: %s", err)
	} else {
		log.Printf("nginx: reload success")
	}
}

func (ngx *Nginx) Run() error {
	if os.Getenv("NO_NGINX") == "1" {
		return nil
	}

	cmd := exec.Command("nginx", "-p", ngx.mainConf.Prefix)

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	ngx.cmd = cmd

	if err := cmd.Run(); err != nil {
		return err
	}

	ngx.stopCh <- struct{}{}
	return nil
}

func (ngx *Nginx) Shutdown() {
	if os.Getenv("NO_NGINX") == "1" {
		return
	}

	if err := ngx.cmd.Process.Signal(syscall.SIGQUIT); err != nil {
		log.Printf("nginx: SIGQUIT: %s", err)
	}

	<-ngx.stopCh
}

func New(mainConf *Config, httpConf *HttpConfig) *Nginx {
	httpConf.Servers = map[string]*ServerConfig{}

	return &Nginx{
		mainConf: mainConf,
		httpConf: httpConf,
		stopCh:   make(chan struct{}),
	}
}