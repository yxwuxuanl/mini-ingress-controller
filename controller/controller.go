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
	"io/ioutil"
	"log"
	"os"
	"path"
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

func getAuthSecretInfo(is *ingress.Ingress) (namespace, name string) {
	if name = is.Metadata.Annotations[annotation.AuthSecret]; name != "" {
		if namespace = is.Metadata.Annotations[annotation.AuthSecretNamespace]; namespace == "" {
			namespace = is.Metadata.Namespace
		}
	}

	return
}

func (c *Controller) setupAuthSecret(namespace, name string, remake bool) (userfile string, err error) {
	sec := new(secret.Secret)
	err = c.secretInformer.Get(namespace, name, secret.ReadFunc(namespace, name), &sec)

	if err != nil {
		return "", fmt.Errorf("get auth secret error: %s", err)
	}

	defer func() {
		if err != nil {
			c.secretInformer.Release(namespace, name)
		}
	}()

	filepath := path.Join(*nginx.Prefix, ngxAuthFileDir, getSecretFilename(sec.Metadata))

	if !remake {
		if _, err := os.Stat(filepath); err == nil {
			return withoutNgxPrefix(filepath), nil
		}
	}

	auth, ok := sec.Data["auth"]

	if !ok {
		return "", errors.New("auth secret missing `auth` key")
	}

	if err = ioutil.WriteFile(filepath, auth, 0777); err != nil {
		return "", err
	}

	return withoutNgxPrefix(filepath), nil
}

func (c *Controller) setupTlsSecret(namespace, name string, remake bool) (crt string, key string, err error) {
	sec := new(secret.Secret)
	err = c.secretInformer.Get(namespace, name, secret.ReadFunc(namespace, name), &sec)

	if err != nil {
		return "", "", err
	}

	defer func() {
		if err != nil {
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
			if err = ioutil.WriteFile(filepath, data, 0777); err != nil {
				return "", err
			}
		}

		return withoutNgxPrefix(filepath), nil
	}

	if key, err = write("tls.key"); err != nil {
		return "", "", err
	}

	if crt, err = write("tls.crt"); err != nil {
		return "", "", err
	}

	return
}

func (c *Controller) addIngress(is *ingress.Ingress) error {
	c.issCache[is.Name()] = is

	var basicAuthConf *nginx.BasicAuthConf

	if ns, name := getAuthSecretInfo(is); name != "" {
		if userfile, err := c.setupAuthSecret(ns, name, false); err != nil {
			return fmt.Errorf("setupAuthSecret: %s, ingress=%s", err, is.Name())
		} else {
			basicAuthConf = &nginx.BasicAuthConf{
				Realm:    "Authentication required",
				UserFile: userfile,
			}
		}
	}

	tlsSecrets := map[string]string{}

	for _, tls := range is.Spec.TLS {
		for _, host := range tls.Host {
			tlsSecrets[host] = tls.SecretName
		}
	}

	for _, rule := range is.Spec.Rules {
		var tlsConfig *nginx.TLSConf

		if sec, ok := tlsSecrets[rule.Host]; ok {
			var err error

			tlsConfig = new(nginx.TLSConf)

			tlsConfig.Cert, tlsConfig.Key, err = c.setupTlsSecret(is.Metadata.Namespace, sec, false)

			if err != nil {
				log.Printf("controller: setupTlsSecret: %s, ingress=%s, host=%s", err, is.Name(), rule.Host)
				continue
			}
		}

		for _, isPath := range rule.Http.Paths {
			loc := &nginx.Location{
				Path: nginx.Path{
					Path:     isPath.Path,
					PathType: isPath.PathType,
				},
				IngressRef:       is.Name(),
				DisableAccessLog: is.Metadata.Annotations[annotation.EnableAccessLog] == "false",
				BasicAuth:        basicAuthConf,
			}

			loc.ProxyPass = &nginx.ProxyPassConf{
				Upstream: fmt.Sprintf(
					"http://%s.%s:%d",
					isPath.Backend.Service.Name,
					is.Metadata.Namespace,
					isPath.Backend.Service.Port.Number,
				),
			}

			if err := c.ngx.AddLocation(rule.Host, loc, tlsConfig); err != nil {
				log.Printf("addIngress: %s, ingress=%s, path=%s", err, is.Name(), loc.Path.String())
			}
		}
	}

	return nil
}

func (c *Controller) deleteIngress(is *ingress.Ingress) {
	for _, rule := range is.Spec.Rules {
		c.ngx.DeleteLocation(rule.Host, is.Name())
	}

	if ns, name := getAuthSecretInfo(is); name != "" {
		c.secretInformer.Release(ns, name)
	}

	for _, tls := range is.Spec.TLS {
		c.secretInformer.Release(is.Metadata.Namespace, tls.SecretName)
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

			if is, ok := c.issCache[is.Name()]; !ok {
				return
			} else {
				c.deleteIngress(is)
			}

			log.Printf("controller: modify ingress %s", is.Name())

			if err := c.addIngress(is); err != nil {
				log.Printf("controller: %s, ingress=%s", err, is.Name())
				return
			}

			buildAndReload()
		},
	}

	go kube.Watch(ctx, c.kc, ingress.WatchFunc, handler)
}

func (c *Controller) Run(ctx context.Context) error {
	var iss []*ingress.Ingress

	if err := kube.List(c.kc, ingress.ListFunc, &iss); err != nil {
		return err
	}

	onSecretModify := func(secret *secret.Secret) {
		_, err := c.setupAuthSecret(secret.Metadata.Namespace, secret.Metadata.Name, true)
		log.Printf("controller: modify secret %s", secret.Name())

		if err != nil {
			log.Printf("controller: setupAuthSecret: %s", err)
			return
		}

		c.ngx.Reload()
	}

	onSecretRelease := func(s *secret.Secret) {
		authfile := path.Join(*nginx.Prefix, ngxAuthFileDir, getSecretFilename(s.Metadata))
		log.Printf("controller: release secret %s", s.Name())

		if err := os.Remove(authfile); err != nil {
			log.Printf("controller: delete authfile error: %s", err)
		}
	}

	c.secretInformer = &kube.Informer[*secret.Secret]{
		Client:    c.kc,
		OnModify:  onSecretModify,
		OnRelease: onSecretRelease,
		WatchFunc: secret.WatchFunc,
	}

	c.secretInformer.Init()

	authfileDir := path.Join(*nginx.Prefix, ngxAuthFileDir)

	if _, err := os.Stat(authfileDir); os.IsNotExist(err) {
		if err := os.Mkdir(authfileDir, 0777); err != nil {
			return err
		}
	}

	for _, is := range iss {
		if ingress.FilterIngress(is) {
			if err := c.addIngress(is); err != nil {
				log.Printf("controller: addIngress: %s, ingrsss=%s", err, is.Name())
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
