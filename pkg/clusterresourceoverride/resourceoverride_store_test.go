package clusterresourceoverride

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

func TestUnstructuredToROView(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": resourceOverrideAPIVersion,
			"kind":       "ResourceOverride",
			"metadata": map[string]interface{}{
				"name":      "example",
				"namespace": "my-namespace",
			},
			"spec": map[string]interface{}{
				"podResourceOverride": map[string]interface{}{
					"memoryRequestToLimitPercent": int64(50),
					"cpuRequestToRequestPercent":  int64(75),
					"limitCPUToMemoryPercent":     int64(100),
				},
				"podSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"workload-type": "batch",
					},
				},
			},
		},
	}

	view, err := unstructuredToROView(u)
	require.NoError(t, err)

	assert.Equal(t, "my-namespace", view.Namespace)
	assert.Equal(t, "example", view.Name)
	require.NotNil(t, view.Selector)
	assert.Equal(t, map[string]string{"workload-type": "batch"}, view.Selector.MatchLabels)
	assert.Equal(t, int64(50), view.Spec.MemoryRequestToLimitPercent)
	assert.Equal(t, int64(75), view.Spec.CPURequestToRequestPercent)
	assert.Equal(t, int64(100), view.Spec.LimitCPUToMemoryPercent)
}

func TestUnstructuredToROViewEmptySelector(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": resourceOverrideAPIVersion,
			"kind":       "ResourceOverride",
			"metadata": map[string]interface{}{
				"name":      "all-pods",
				"namespace": "app-ns",
			},
			"spec": map[string]interface{}{
				"podResourceOverride": map[string]interface{}{
					"cpuRequestToLimitPercent": int64(80),
				},
			},
		},
	}

	view, err := unstructuredToROView(u)
	require.NoError(t, err)

	assert.Equal(t, "app-ns", view.Namespace)
	assert.Equal(t, "all-pods", view.Name)
	assert.Nil(t, view.Selector)
	assert.Equal(t, int64(80), view.Spec.CPURequestToLimitPercent)
}

func TestUnstructuredToUsableConfig(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": resourceOverrideAPIVersion,
			"kind":       "ResourceOverride",
			"metadata": map[string]interface{}{
				"name":      "ibm-z",
				"namespace": "was-ns",
			},
			"spec": map[string]interface{}{
				"podResourceOverride": map[string]interface{}{
					"limitCPUToMemoryPercent":     int64(400),
					"cpuRequestToLimitPercent":    int64(25),
					"memoryRequestToLimitPercent": int64(50),
					"cpuRequestToRequestPercent":  int64(75),
				},
			},
		},
	}

	view, err := unstructuredToROView(u)
	require.NoError(t, err)

	config := configFromROView(view)
	require.NotNil(t, config)
	assert.Equal(t, 4.0, config.LimitCPUToMemoryRatio)
	assert.Equal(t, 0.25, config.CpuRequestToLimitRatio)
	assert.Equal(t, 0.50, config.MemoryRequestToLimitRatio)
	assert.Equal(t, 0.75, config.CpuRequestToRequestRatio)
}

func TestResourceOverrideStoreListByNamespaceExempt(t *testing.T) {
	store := &resourceOverrideStore{}
	views, err := store.ListByNamespace("openshift")
	require.NoError(t, err)
	assert.Empty(t, views)
}

func TestResourceOverrideStoreListByNamespaceNilStore(t *testing.T) {
	var store *resourceOverrideStore
	views, err := store.ListByNamespace("app-ns")
	require.NoError(t, err)
	assert.Nil(t, views)
}

func TestResourceOverrideStoreListByNamespaceFromIndexer(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": resourceOverrideAPIVersion,
			"kind":       "ResourceOverride",
			"metadata": map[string]interface{}{
				"name":              "ro-a",
				"namespace":         "app-ns",
				"resourceVersion":   "1",
				"uid":               "uid-a",
				"creationTimestamp": metav1.Now().Format("2006-01-02T15:04:05Z"),
			},
			"spec": map[string]interface{}{
				"podResourceOverride": map[string]interface{}{
					"memoryRequestToLimitPercent": int64(50),
				},
			},
		},
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})
	require.NoError(t, indexer.Add(u))

	store := &resourceOverrideStore{indexer: indexer}
	views, err := store.ListByNamespace("app-ns")
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, "ro-a", views[0].Name)
	assert.Equal(t, int64(50), views[0].Spec.MemoryRequestToLimitPercent)
}
