package controller

import (
	"context"
	"errors"
	"fmt"
	"ingress-controller/controller/annotation"
	"ingress-controller/kube"
	"ingress-controller/kube/ingress"
	"ingress-controller/kube/secret"
	"ingress-controller/nginx"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	ngxAuthFileDir = "authfiles/"
	ngxTlsDir      = "tls/"
)

type Controller struct {
	issCache       map[string]*ingress.Ingress
	ngx            *nginx.Nginx
	kc             kube.Client
	secretInformer *kube.Informer[*secret.Secret]
}

func getSecretFilename(mt *kube.Metadata) string {
	return mt.Namespace + "-" + mt.Name
}

func withoutNgxPrefix(p string) string {
	return strings.TrimPrefix(p, *nginx.Prefix+"/")
}

func (c *Controller) setupAuthSecret(namespace, name string, remake bool) (userfile string, err error) {
	sec := new(secret.Secret)
	err = c.secretInformer.Get(namespace, name, secret.ReadFunc(namespace, name), &sec)

	if err != nil {
		return
	}

	defer func() {
		if remake || err != nil {
			c.secretInformer.Release(namespace, name)
		}
	}()

	userfile = path.Join(ngxAuthFileDir, getSecretFilename(sec.Metadata))

	filepath := path.Join(*nginx.Prefix, userfile)

	if !remake {
		if _, err = os.Stat(filepath); err == nil {
			return
		}
	}

	auth, ok := sec.Data["auth"]

	if !ok {
		err = errors.New("auth secret missing `auth` key")
		return
	}

	if err = os.WriteFile(filepath, auth, 0777); err != nil {
		return
	}

	return
}

func (c *Controller) setupTlsSecret(namespace, name string, remake bool) (crt string, key string, err error) {
	sec := new(secret.Secret)
	err = c.secretInformer.Get(namespace, name, secret.ReadFunc(namespace, name), &sec)

	if err != nil {
		return
	}

	defer func() {
		if remake || err != nil {
			c.secretInformer.Release(namespace, name)
		}
	}()

	write := func(key string) (string, error) {
		data := sec.Data[key]

		if data == nil {
			return "", fmt.Errorf("tls secret missing `%s` key", key)
		}

		filepath := path.Join(
			*nginx.Prefix,
			ngxTlsDir,
			getSecretFilename(sec.Metadata),
		)

		if _, err := os.Stat(filepath); err != nil {
			if os.IsNotExist(err) {
				err = os.MkdirAll(filepath, 0777)
			}

			if err != nil {
				return "", err
			}
		}

		filepath = path.Join(filepath, key)

		if _, err = os.Stat(filepath); err != nil || remake {
			if err = os.WriteFile(filepath, data, 0777); err != nil {
				return "", err
			}
		}

		return withoutNgxPrefix(filepath), nil
	}

	if key, err = write("tls.key"); err != nil {
		return
	}

	if crt, err = write("tls.crt"); err != nil {
		return
	}

	return
}

func (c *Controller) addIngress(is *ingress.Ingress) error {
	log.Printf("controller: add ingress %s", is.Name())

	c.issCache[is.Name()] = is

	var basicAuthConf *nginx.BasicAuthConf

	if ns, name, ok := annotation.ParseAuthSecret(is); ok {
		if userfile, err := c.setupAuthSecret(ns, name, false); err != nil {
			return fmt.Errorf("setupAuthSecret: %s", err)
		} else {
			basicAuthConf = &nginx.BasicAuthConf{
				Realm:    "Authentication required",
				UserFile: userfile,
			}
		}
	}

	var directives []nginx.Directive

	setProxyTimeout := func(key string) {
		if v, ok := is.Metadata.Annotations[fmt.Sprintf(annotation.Prefix+"proxy-%s-timeout", key)]; ok {
			if iv, _ := strconv.Atoi(v); iv > 0 {
				directives = append(directives, nginx.Directive{
					fmt.Sprintf("proxy_%s_timeout", key),
					v + "s",
				})
			}
		}
	}

	setProxyTimeout("read")
	setProxyTimeout("connect")
	setProxyTimeout("send")

	tlsConfs := map[string]*nginx.TLSConf{}

	getTlsConf := func(host string) (*nginx.TLSConf, error) {
		var secretName string

		for _, tls := range is.Spec.TLS {
			for _, h := range tls.Host {
				if h == host {
					secretName = tls.SecretName
					break
				}
			}
		}

		if secretName == "" {
			return nil, nil
		}

		if conf, ok := tlsConfs[secretName]; ok {
			return conf, nil
		}

		var err error

		tlsConfig := new(nginx.TLSConf)
		tlsConfig.Cert, tlsConfig.Key, err = c.setupTlsSecret(is.Metadata.Namespace, secretName, false)

		if err != nil {
			return nil, err
		}

		tlsConfs[secretName] = tlsConfig
		return tlsConfig, nil
	}

	for _, rule := range is.Spec.Rules {
		tlsConfig, err := getTlsConf(rule.Host)

		if err != nil {
			log.Printf("controller: getTlsConf error: %s, host=%s", err, rule.Host)
			continue
		}

		for _, isPath := range rule.Http.Paths {
			loc := &nginx.Location{
				Path: nginx.Path{
					Path:     isPath.Path,
					PathType: isPath.PathType,
				},
				Directives:       directives,
				IngressRef:       is.Name(),
				DisableAccessLog: is.Metadata.Annotations[annotation.EnableAccessLog] == "false",
				BasicAuth:        basicAuthConf,
			}

			if v, ok := is.Metadata.Annotations[annotation.UseRegex]; ok {
				loc.Path.Regex = v == "true"
			}

			if rewrite, ok := is.Metadata.Annotations[annotation.RewriteTarget]; ok {
				loc.Return = &nginx.ReturnConf{Code: 301, Text: rewrite}
			} else {
				loc.ProxyPass = &nginx.ProxyPassConf{
					Upstream: fmt.Sprintf(
						"http://%s.%s:%d",
						isPath.Backend.Service.Name,
						is.Metadata.Namespace,
						isPath.Backend.Service.Port.Number,
					),
				}
			}

			if err = c.ngx.AddLocation(rule.Host, loc, tlsConfig); err != nil {
				log.Printf("addIngress: %s, ingress=%s, path=%s", err, is.Name(), loc.Path.String())
			}
		}

		if tlsConfig != nil && is.Metadata.Annotations[annotation.ForceSSLRedirect] == "true" {
			err := c.ngx.AddLocation(rule.Host, &nginx.Location{
				Path: nginx.Path{
					Path:     "/",
					PathType: ingress.PathTypePrefix,
				},
				Return: &nginx.ReturnConf{
					Code: 301,
					Text: "https://$host$request_uri",
				},
				IngressRef: is.Name(),
			}, nil)

			if err != nil {
				log.Printf("controller: add ssl redirect rule error: %s, ingress=%s, host=%s", err, is.Name(), rule.Host)
			}
		}
	}

	return nil
}

func (c *Controller) deleteIngress(is *ingress.Ingress) {
	log.Printf("controller: delete ingress %s", is.Name())

	for _, rule := range is.Spec.Rules {
		c.ngx.DeleteLocation(rule.Host, is.Name())
	}

	if ns, name, ok := annotation.ParseAuthSecret(is); ok {
		c.secretInformer.Release(ns, name)
	}

	isRelease := map[string]struct{}{}

	for _, tls := range is.Spec.TLS {
		if _, ok := isRelease[tls.SecretName]; !ok {
			c.secretInformer.Release(is.Metadata.Namespace, tls.SecretName)
			isRelease[tls.SecretName] = struct{}{}
		}
	}

	delete(c.issCache, is.Name())
}

func (c *Controller) watch(ctx context.Context) {
	buildAndReload := func() {
		if err := c.ngx.BuildHttpConfig(); err != nil {
			log.Printf("controller: BuildHttpConfig: %s", err)
		} else {
			c.ngx.Reload()
		}
	}

	handler := kube.WatchHandler[*ingress.Ingress]{
		Added: func(is *ingress.Ingress) {
			if !ingress.FilterIngress(is) {
				return
			}

			if _, ok := c.issCache[is.Name()]; !ok {
				if err := c.addIngress(is); err != nil {
					log.Printf("controller: %s, ingress=%s", err, is.Name())
				} else {
					log.Printf("controller: add ingress %s", is.Name())
					buildAndReload()
				}
			}
		},
		Deleted: func(is *ingress.Ingress) {
			if !ingress.FilterIngress(is) {
				return
			}

			if _, ok := c.issCache[is.Name()]; ok {
				log.Printf("controller: delete ingress %s", is.Name())
				c.deleteIngress(is)
				buildAndReload()
			}
		},
		Modified: func(is *ingress.Ingress) {
			if !ingress.FilterIngress(is) {
				return
			}

			log.Printf("controller: modify ingress %s", is.Name())

			if is, ok := c.issCache[is.Name()]; !ok {
				return
			} else {
				c.deleteIngress(is)
			}

			if err := c.addIngress(is); err != nil {
				log.Printf("controller: %s, ingress=%s", err, is.Name())
				return
			}

			buildAndReload()
		},
	}

	go kube.Watch(ctx, c.kc, ingress.WatchFunc, handler)
}

func (c *Controller) setupSecretInformer() {
	onModify := func(sec *secret.Secret) {
		mt := sec.Metadata

		switch sec.Type {
		case secret.TypeOpaque:
			userfile, err := c.setupAuthSecret(mt.Namespace, mt.Name, true)

			if err != nil {
				log.Printf("controller: setupAuthSecret: %s", err)
				return
			}

			log.Printf("controller: userfile %s updated", userfile)
		case secret.TypeTLS:
			crt, _, err := c.setupTlsSecret(mt.Namespace, mt.Name, true)

			if err != nil {
				log.Printf("controller: setupTlsSecret: %s", err)
				return
			}

			log.Printf("controller: tls %s updated", crt)
		default:
			return
		}

		c.ngx.Reload()
	}

	onRelease := func(sec *secret.Secret) {
		var files string

		switch sec.Type {
		case secret.TypeOpaque:
			files = path.Join(*nginx.Prefix, ngxAuthFileDir, getSecretFilename(sec.Metadata))
		case secret.TypeTLS:
			files = path.Join(*nginx.Prefix, ngxTlsDir, getSecretFilename(sec.Metadata))
		default:
			return
		}

		if err := os.RemoveAll(files); err != nil {
			log.Printf("controller: delete secret error: %s", err)
		}
	}

	c.secretInformer = &kube.Informer[*secret.Secret]{
		Client:    c.kc,
		OnModify:  onModify,
		OnRelease: onRelease,
		WatchFunc: secret.WatchFunc,
	}

	c.secretInformer.Init()
}

func (c *Controller) Run(ctx context.Context) error {
	var iss []*ingress.Ingress

	if err := kube.List(c.kc, ingress.ListFunc, &iss); err != nil {
		return err
	}

	authfileDir := path.Join(*nginx.Prefix, ngxAuthFileDir)

	if _, err := os.Stat(authfileDir); os.IsNotExist(err) {
		if err := os.Mkdir(authfileDir, 0777); err != nil {
			return err
		}
	}

	c.setupSecretInformer()

	for _, is := range iss {
		if ingress.FilterIngress(is) {
			if err := c.addIngress(is); err != nil {
				log.Printf("controller: %s, ingrsss=%s", err, is.Name())
			}
		}
	}

	if err := c.ngx.BuildHttpConfig(); err != nil {
		return err
	}

	go c.watch(ctx)
	go c.secretInformer.Run(ctx)

	return c.ngx.Run()
}

func (c *Controller) Shutdown() {
	c.ngx.Shutdown()
}

func New(ngx *nginx.Nginx, kc kube.Client) *Controller {
	return &Controller{
		issCache: map[string]*ingress.Ingress{},
		ngx:      ngx,
		kc:       kc,
	}
}
