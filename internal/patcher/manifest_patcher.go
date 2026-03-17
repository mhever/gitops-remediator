package patcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

// ManifestPatcher implements Patcher using filesystem YAML manipulation.
type ManifestPatcher struct{}

// NewManifestPatcher creates a ManifestPatcher.
func NewManifestPatcher() *ManifestPatcher {
	return &ManifestPatcher{}
}

var _ Patcher = (*ManifestPatcher)(nil)

// Apply locates the manifest for the pod's deployment, applies the appropriate
// patch based on diag.PatchType, validates the resulting YAML, and writes it back.
func (p *ManifestPatcher) Apply(ctx context.Context, repoDir string, diag diagnostician.Diagnosis, event watcher.FailureEvent) (*PatchResult, error) {
	name := deploymentName(event.PodName)

	var (
		filePath   string
		oldContent []byte
		newContent []byte
		err        error
	)

	switch diag.PatchType {
	case "image_tag":
		imageName := name
		kustomizationPath, findErr := findKustomization(repoDir, imageName)
		if findErr != nil {
			return nil, fmt.Errorf("patcher: find kustomization: %w", findErr)
		}
		if kustomizationPath != "" {
			// Kustomize path: read the kustomization file and patch it
			oldContent, err = os.ReadFile(kustomizationPath)
			if err != nil {
				return nil, fmt.Errorf("patcher: read kustomization: %w", err)
			}
			filePath = kustomizationPath
			newContent, err = applyKustomizationImageTag(oldContent, imageName, diag.PatchValue)
		} else {
			// Fallback: find and patch image: line in deployment YAML
			filePath, err = findManifest(repoDir, name)
			if err != nil {
				return nil, fmt.Errorf("patcher: %w", err)
			}
			oldContent, err = os.ReadFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("patcher: read manifest: %w", err)
			}
			newContent, err = applyImageTag(oldContent, event.ContainerName, diag.PatchValue)
		}
	default:
		filePath, err = findManifest(repoDir, name)
		if err != nil {
			return nil, fmt.Errorf("patcher: %w", err)
		}
		oldContent, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("patcher: read manifest: %w", err)
		}
		switch diag.PatchType {
		case "memory_limit":
			newContent, err = applyMemoryLimit(oldContent, event.ContainerName, diag.PatchValue)
		case "env_var":
			newContent, err = applyEnvVar(oldContent, diag.PatchValue)
		default:
			return nil, fmt.Errorf("patcher: unknown patch type %q", diag.PatchType)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("patcher: apply patch: %w", err)
	}

	// Validate the patched YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(newContent, &parsed); err != nil {
		return nil, fmt.Errorf("patcher: patched YAML is invalid: %w", err)
	}

	if err := os.WriteFile(filePath, newContent, 0644); err != nil {
		return nil, fmt.Errorf("patcher: write manifest: %w", err)
	}

	relPath, err := filepath.Rel(repoDir, filePath)
	if err != nil {
		return nil, fmt.Errorf("patcher: compute relative path: %w", err)
	}

	diff := unifiedDiff(oldContent, newContent, relPath)

	return &PatchResult{
		FilePath:   relPath,
		OldContent: oldContent,
		NewContent: newContent,
		Diff:       diff,
	}, nil
}
