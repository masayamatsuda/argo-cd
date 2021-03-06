package argo

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/argoproj/argo-cd/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/util/diff"
	"github.com/argoproj/argo-cd/util/settings"

	jsonpatch "github.com/evanphx/json-patch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	noMatchingPathError = "Unable to remove nonexistent key: not-matching-path"
)

type normalizerPatch struct {
	groupKind schema.GroupKind
	namespace string
	name      string
	patch     jsonpatch.Patch
}

type normalizer struct {
	patches []normalizerPatch
}

type overrideIgnoreDiff struct {
	JsonPointers []string `yaml:"jsonPointers"`
}

// NewDiffNormalizer creates diff normalizer which removes ignored fields according to given application spec and resource overrides
func NewDiffNormalizer(ignore []v1alpha1.ResourceIgnoreDifferences, overrides map[string]settings.ResourceOverride) (diff.Normalizer, error) {
	for key, override := range overrides {
		parts := strings.Split(key, "/")
		if len(parts) < 2 {
			continue
		}
		group := parts[0]
		kind := parts[1]
		if override.IgnoreDifferences != "" {
			ignoreSettings := overrideIgnoreDiff{}
			err := yaml.Unmarshal([]byte(override.IgnoreDifferences), &ignoreSettings)
			if err != nil {
				return nil, err
			}

			ignore = append(ignore, v1alpha1.ResourceIgnoreDifferences{
				Group:        group,
				Kind:         kind,
				JsonPointers: ignoreSettings.JsonPointers,
			})
		}
	}
	patches := make([]normalizerPatch, 0)
	for i := range ignore {
		for _, path := range ignore[i].JsonPointers {
			patchData, err := json.Marshal([]map[string]string{{"op": "remove", "path": path}})
			if err != nil {
				return nil, err
			}
			patch, err := jsonpatch.DecodePatch(patchData)
			if err != nil {
				return nil, err
			}
			patches = append(patches, normalizerPatch{
				groupKind: schema.GroupKind{Group: ignore[i].Group, Kind: ignore[i].Kind},
				name:      ignore[i].Name,
				namespace: ignore[i].Namespace,
				patch:     patch,
			})
		}

	}
	return &normalizer{patches: patches}, nil
}

// Normalize removes fields from supplied resource using json paths from matching items of specified resources ignored differences list
func (n *normalizer) Normalize(un *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	matched := make([]normalizerPatch, 0)
	for _, patch := range n.patches {
		groupKind := un.GroupVersionKind().GroupKind()
		if groupKind == patch.groupKind &&
			(patch.name == "" || patch.name == un.GetName()) &&
			(patch.namespace == "" || patch.namespace == un.GetNamespace()) {

			matched = append(matched, patch)
		}
	}
	if len(matched) == 0 {
		return un, nil
	}

	docData, err := json.Marshal(un)
	if err != nil {
		return nil, err
	}

	for _, patch := range matched {
		patchedData, err := patch.patch.Apply(docData)
		if err != nil {
			if err.Error() == noMatchingPathError {
				continue
			}
			return nil, err
		}
		docData = patchedData
	}

	err = json.Unmarshal(docData, un)
	if err != nil {
		return nil, err
	}
	return un, nil
}
