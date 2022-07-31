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
)

const ngxAuthFileDir = "authfiles/"

type Controller struct {
	issCache map[string]*ingress.Ingress
	ngx      *nginx.Nginx
	kc       kube.Client
	sm       *secret.Manager
}

func getAuthFilename(sec *secret.Secret) string {
	return sec.Metadata.Namespace + "." + sec.Metadata.Name
}

func (c *Controller) setupAuthSecret(namespace, name string, remake bool) (string, error) {
	sec, err := c.sm.Get(namespace, name)

	if err != nil {
		return "", fmt.Errorf("get auth secret error: %s", err)
	}

	filename := getAuthFilename(sec)
	authFile := path.Join(*nginx.Prefix, ngxAuthFileDir, filename)

	if !remake {
		if _, err := os.Stat(authFile); err == nil {
			return path.Join(ngxAuthFileDir, filename), nil
		}
	}

	auth, ok := sec.Data["auth"]

	if !ok {
		return "", errors.New("auth secret missing `auth` key")
	}

	if err = ioutil.WriteFile(authFile, auth, 0777); err != nil {
		return "", err
	}

	return path.Join(ngxAuthFileDir, filename), nil
}

func (c *Controller) addIngress(is *ingress.Ingress) error {
	c.issCache[is.Name()] = is

	var basicAuthConf *nginx.BasicAuthConf

	if v, ok := is.Metadata.Annotations[annotation.AuthSecret]; ok {
		if userfile, err := c.setupAuthSecret(is.Metadata.Namespace, v, false); err != nil {
			return fmt.Errorf("setupAuthSecret: %s, ingress=%s", err, is.Name())
		} else {
			basicAuthConf = &nginx.BasicAuthConf{
				Realm:    "Authentication required",
				UserFile: userfile,
			}
		}
	}

	for _, rule := range is.Spec.Rules {
		for _, p := range rule.Http.Paths {
			loc := &nginx.Location{
				Path: nginx.Path{
					Path:     p.Path,
					PathType: p.PathType,
				},
				IngressRef:       is.Name(),
				DisableAccessLog: is.Metadata.Annotations[annotation.EnableAccessLog] == "false",
				BasicAuth:        basicAuthConf,
			}

			loc.ProxyPass = &nginx.ProxyPassConf{
				Upstream: fmt.Sprintf(
					"http://%s.%s:%d/",
					p.Backend.Service.Name,
					is.Metadata.Namespace,
					p.Backend.Service.Port.Number,
				),
			}

			if err := c.ngx.AddLocation(rule.Host, loc); err != nil {
				log.Printf("addIngress: %s, ingress=%s", err, is.Name())
			}
		}
	}

	return nil
}

func (c *Controller) deleteIngress(is *ingress.Ingress) {
	for _, rule := range is.Spec.Rules {
		c.ngx.DeleteLocation(rule.Host, is.Name())
	}

	if v, ok := is.Metadata.Annotations[annotation.AuthSecret]; ok {
		c.sm.Release(is.Metadata.Namespace, v)
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
					buildAndReload()
				}
			}
		},
		Deleted: func(is *ingress.Ingress) {
			if !ingress.FilterIngress(is) {
				return
			}

			if _, ok := c.issCache[is.Name()]; ok {
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

		if err != nil {
			log.Printf("controller: setupAuthSecret: %s", err)
			return
		}

		c.ngx.Reload()
	}

	onSecretRelease := func(secret *secret.Secret) {
		authfile := path.Join(*nginx.Prefix, ngxAuthFileDir, getAuthFilename(secret))
		if err := os.Remove(authfile); err != nil {
			log.Printf("controller: delete authfile error: %s", err)
		}
	}

	sm := secret.NewSecretManager(c.kc, onSecretModify, onSecretRelease)
	c.sm = sm

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
	go sm.Run(ctx)

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
