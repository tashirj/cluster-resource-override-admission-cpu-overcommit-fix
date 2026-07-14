package clusterresourceoverride

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog"
)

const resourceOverrideAPIVersion = "autoscaling.openshift.io/v1"

var resourceOverrideGVR = schema.GroupVersionResource{
	Group:    "autoscaling.openshift.io",
	Version:  "v1",
	Resource: "resourceoverrides",
}

// ResourceOverrideSpec is a local mirror of the ResourceOverride CR spec.
// CRO does not import CROO generated clients.
type ResourceOverrideSpec struct {
	PodResourceOverride ClusterResourceOverrideSpec `json:"podResourceOverride"`
	PodSelector         *metav1.LabelSelector       `json:"podSelector,omitempty"`
}

// ResourceOverrideView is a local projection of a cached ResourceOverride object.
type ResourceOverrideView struct {
	Namespace string
	Name      string
	Selector  *metav1.LabelSelector
	Spec      ClusterResourceOverrideSpec
}

// ResourceOverrideLister lists cached ResourceOverride objects for a namespace.
type ResourceOverrideLister interface {
	ListByNamespace(namespace string) ([]ResourceOverrideView, error)
}

type resourceOverrideStore struct {
	indexer cache.Indexer
}

func unstructuredToROView(u *unstructured.Unstructured) (ResourceOverrideView, error) {
	raw, err := json.Marshal(u.Object)
	if err != nil {
		return ResourceOverrideView{}, err
	}
	var wrapper struct {
		Spec ResourceOverrideSpec `json:"spec"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return ResourceOverrideView{}, err
	}
	return ResourceOverrideView{
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		Selector:  wrapper.Spec.PodSelector,
		Spec:      wrapper.Spec.PodResourceOverride,
	}, nil
}

// configFromROView converts a cached ResourceOverride into the internal Config
// used by the mutator.
func configFromROView(view ResourceOverrideView) *Config {
	return ConvertExternalConfig(&ClusterResourceOverride{
		Spec: view.Spec,
	})
}

func NewResourceOverrideStore(
	dynFactory dynamicinformer.DynamicSharedInformerFactory,
	stopCh <-chan struct{},
) (ResourceOverrideLister, error) {
	informer := dynFactory.ForResource(resourceOverrideGVR).Informer()

	if err := informer.AddIndexers(cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	}); err != nil {
		return nil, fmt.Errorf("add namespace index on ResourceOverride informer: %w", err)
	}

	go informer.Run(stopCh)

	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		return nil, fmt.Errorf("ResourceOverride informer cache sync failed")
	}

	klog.Infof("name=%s ResourceOverride informer started GVR=%s", Name, resourceOverrideGVR)
	return &resourceOverrideStore{indexer: informer.GetIndexer()}, nil
}

// NewResourceOverrideListerOrNil soft-fails when the CRD is missing or the informer cannot start.
// Admission continues with ClusterResourceOverride-only behavior.
func NewResourceOverrideListerOrNil(cfg *restclient.Config, stopCh <-chan struct{}) ResourceOverrideLister {
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		klog.Warningf("name=%s ResourceOverride informer disabled: %v", Name, err)
		return nil
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		dynClient,
		defaultResyncPeriod,
		metav1.NamespaceAll,
		nil,
	)
	store, err := NewResourceOverrideStore(factory, stopCh)
	if err != nil {
		klog.Warningf("name=%s ResourceOverride informer disabled: %v", Name, err)
		return nil
	}
	return store
}

func (s *resourceOverrideStore) ListByNamespace(ns string) ([]ResourceOverrideView, error) {
	if s == nil || s.indexer == nil {
		return nil, nil
	}

	if IsNamespaceExempt(ns) {
		return nil, nil
	}

	items, err := s.indexer.ByIndex(cache.NamespaceIndex, ns)
	if err != nil {
		return nil, err
	}

	out := make([]ResourceOverrideView, 0, len(items))
	for _, item := range items {
		u, ok := item.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		view, err := unstructuredToROView(u)
		if err != nil {
			klog.Warningf("skip malformed ResourceOverride %s/%s: %v", ns, u.GetName(), err)
			continue
		}
		if IsNamespaceExempt(view.Namespace) {
			continue
		}
		out = append(out, view)
	}
	return out, nil
}
