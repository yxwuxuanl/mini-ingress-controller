package controller

import (
	"encoding/json"
	"fmt"
	"ingress-controller/ingress"
	"ingress-controller/nginx"
	"log"
	"net/url"
)

type Controller struct {
	cache map[string]*ingress.Ingress
	ngx   *nginx.Nginx
}

func (c *Controller) addNgxLocation(is *ingress.Ingress, rules []*ingress.Rule) {
	for _, rule := range rules {
		for _, path := range rule.Http.Paths {
			if err := c.ngx.AddLocation(rule.Host, path2Location(is, path)); err != nil {
				log.Printf("controller: add %s, ingress=%s, err=%s", path2Json(path), is.Metadata.FullName(), err)
			} else {
				log.Printf("controller: add %s, ingress=%s", path2Json(path), is.Metadata.FullName())
			}
		}
	}
}

func (c *Controller) deleteNgxLocation(is *ingress.Ingress, rules []*ingress.Rule) {
	for _, rule := range rules {
		for _, path := range rule.Http.Paths {
			log.Printf("controller: delete %s, ingress=%s", path2Json(path), is.Metadata.FullName())
			c.ngx.DeleteLocation(rule.Host, path2Location(is, path))
		}
	}
}

func (c *Controller) Added(is *ingress.Ingress) bool {
	fullname := is.Metadata.FullName()

	if _, ok := c.cache[fullname]; !ok {
		c.cache[fullname] = is
		c.addNgxLocation(is, is.Spec.Rules)
		return true
	}

	return false
}

func (c *Controller) Delete(is *ingress.Ingress) bool {
	fullname := is.Metadata.FullName()

	if _is, ok := c.cache[fullname]; ok {
		c.deleteNgxLocation(_is, _is.Spec.Rules)
		delete(c.cache, fullname)
		return true
	}

	return false
}

func (c *Controller) Modify(is *ingress.Ingress) bool {
	fullname := is.Metadata.FullName()
	oldIs, ok := c.cache[fullname]

	if !ok {
		return c.Added(is)
	}

	c.deleteNgxLocation(oldIs, oldIs.Spec.Rules)
	c.addNgxLocation(is, is.Spec.Rules)
	return true
}

func New(ngx *nginx.Nginx) *Controller {
	return &Controller{
		cache: map[string]*ingress.Ingress{},
		ngx:   ngx,
	}
}

func path2Location(is *ingress.Ingress, path *ingress.Path) *nginx.Location {
	svc := path.Backend.Service

	var pathType string

	if path.PathType == ingress.PathTypeExact {
		pathType = ingress.PathTypeExact
	} else {
		pathType = ingress.PathTypePrefix
	}

	if path.Path == "" {
		path.Path = "/"
	}

	ups, _ := url.Parse(fmt.Sprintf("http://%s.%s:%d", svc.Name, is.Metadata.Namespace, svc.Port.Number))

	return &nginx.Location{
		Path: nginx.Path{
			Path:     path.Path,
			PathType: pathType,
		},
		IngressRef: is.Metadata.FullName(),
		Upstream:   ups,
	}
}

func path2Json(path *ingress.Path) string {
	data, _ := json.Marshal(path)
	return string(data)
}
