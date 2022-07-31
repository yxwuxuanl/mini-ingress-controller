package kube

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

const serviceaccountMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"

const (
	EventAdd    = "ADDED"
	EventDelete = "DELETED"
	EventModify = "MODIFIED"
)

type Object interface {
	Name() string
}

type Metadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Uid               string            `json:"uid"`
	ResourceVersion   string            `json:"resourceVersion"`
	Generation        int               `json:"generation"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	Annotations       map[string]string `json:"annotations"`
}

type ReadFunc func(r *http.Request)

type Event[T any] struct {
	Type   string `json:"type"`
	Object T      `json:"object"`
}

type WatchHandler[T Object] struct {
	Added    func(T)
	Deleted  func(T)
	Modified func(T)
}

func List[T any](client Client, listFunc ReadFunc, items *[]T) error {
	r := client.Request()
	listFunc(r)

	res, err := client.Do(r)

	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return errors.New("list: http: " + res.Status)
	}

	dec := json.NewDecoder(res.Body)
	defer res.Body.Close()

	list := new(struct {
		Items []T `json:"items"`
	})

	if err := dec.Decode(list); err != nil {
		return err
	}

	*items = list.Items
	return nil
}

func Get[T Object](client Client, listFunc ReadFunc, obj T) error {
	r := client.Request()
	listFunc(r)

	res, err := client.Do(r)

	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return errors.New("http: " + res.Status)
	}

	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(obj)
}

func Watch[T Object](
	ctx context.Context,
	client Client,
	watchFunc ReadFunc,
	handler WatchHandler[T],
) {
	eventCh := make(chan Event[T])
	defer close(eventCh)

	doWatch := func() error {
		r := client.Request()
		watchFunc(r)

		log.Printf("watch: watch %s", r.URL.Path)
		res, err := client.Do(r.WithContext(ctx))

		if err != nil {
			return err
		}

		if res.StatusCode != http.StatusOK {
			return errors.New("http: " + res.Status)
		}

		reader := bufio.NewReader(res.Body)
		defer res.Body.Close()

		for {
			chunk, _, err := reader.ReadLine()

			if err != nil {
				return err
			}

			event := Event[T]{}

			if err := json.Unmarshal(chunk, &event); err != nil {
				err = errors.New("watch: unmarshal event: " + err.Error())
				continue
			}

			log.Printf("watch: %s %s", event.Type, event.Object.Name())
			eventCh <- event
		}
	}

	go func() {
		for event := range eventCh {
			switch event.Type {
			case EventModify:
				if handler.Modified != nil {
					handler.Modified(event.Object)
				}
			case EventAdd:
				if handler.Added != nil {
					handler.Added(event.Object)
				}
			case EventDelete:
				if handler.Deleted != nil {
					handler.Deleted(event.Object)
				}
			}
		}
	}()

	for {
		if err := doWatch(); !errors.Is(err, context.Canceled) {
			log.Printf("watch: %s", err)
			time.Sleep(time.Second * 5)
			continue
		}

		return
	}
}
