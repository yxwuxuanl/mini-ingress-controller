package secret

import (
	"context"
	"ingress-controller/kube"
)

type Cache struct {
	refCount int
	secret   *Secret
}

type Manager struct {
	kc        kube.Client
	cache     map[string]*Cache
	onModify  func(secret *Secret)
	onRelease func(secret *Secret)
}

func (sm *Manager) Get(namespace, name string) (*Secret, error) {
	fullname := namespace + "/" + name

	if ref, ok := sm.cache[fullname]; ok {
		ref.refCount++
		return ref.secret, nil
	}

	sec := new(Secret)

	if err := kube.Get(sm.kc, ReadFunc(namespace, name), sec); err != nil {
		return nil, err
	}

	ref := &Cache{
		refCount: 1,
		secret:   sec,
	}

	sm.cache[fullname] = ref
	return sec, nil
}

func (sm *Manager) Release(namespace, name string) {
	fullname := namespace + "/" + name

	if ref, ok := sm.cache[fullname]; ok {
		if ref.refCount <= 1 {
			sm.onRelease(ref.secret)
			delete(sm.cache, fullname)
		} else {
			ref.refCount--
		}
	}
}

func (sm *Manager) Run(ctx context.Context) {
	watchHandler := kube.WatchHandler[*Secret]{
		Modified: func(secret *Secret) {
			if _, ok := sm.cache[secret.Name()]; ok {
				sm.cache[secret.Name()].secret = secret
				sm.onModify(secret)
			}
		},
		Deleted: func(secret *Secret) {
			delete(sm.cache, secret.Name())
		},
	}

	kube.Watch(ctx, sm.kc, WatchFunc, watchHandler)
}

func NewSecretManager(
	kc kube.Client,
	onModify func(*Secret),
	onRelease func(*Secret),
) *Manager {
	return &Manager{
		kc:        kc,
		cache:     map[string]*Cache{},
		onModify:  onModify,
		onRelease: onRelease,
	}
}
