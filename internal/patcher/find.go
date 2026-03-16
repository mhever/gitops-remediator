package patcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// deploymentName extracts the base deployment name from a pod name.
// Pod names follow the pattern <deployment>-<rs-hash>-<pod-hash>.
// If the pattern doesn't match, the pod name is returned unchanged.
func deploymentName(podName string) string {
	// Strip pod hash suffix
	idx := strings.LastIndex(podName, "-")
	if idx == -1 {
		return podName
	}
	result := podName[:idx]

	// Strip replicaset hash suffix
	idx = strings.LastIndex(result, "-")
	if idx == -1 {
		return result
	}
	return result[:idx]
}

// containsDeploymentWithName returns true if content contains a Deployment or
// StatefulSet resource whose metadata.name equals name. It verifies the name
// appears as "  name: <name>" (2-space indent) after a kind: Deployment/StatefulSet
// line, matching the standard Kubernetes manifest structure.
func containsDeploymentWithName(content, name string) bool {
	lines := strings.Split(content, "\n")
	foundKind := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "kind: Deployment" || trimmed == "kind: StatefulSet" {
			foundKind = true
		}
		// metadata.name is indented with exactly 2 spaces in standard k8s manifests
		if foundKind && line == "  name: "+name {
			return true
		}
	}
	return false
}

// findManifest walks repoDir recursively, looking for a .yaml or .yml file
// that contains a Deployment or StatefulSet with metadata.name equal to name.
// Returns the absolute file path of the first match, or an error if none found.
func findManifest(repoDir, name string) (string, error) {
	var found string
	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		contentBytes, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable files
		}
		content := string(contentBytes)

		if containsDeploymentWithName(content, name) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("findManifest: walk error: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("no manifest found for deployment %q in %s", name, repoDir)
	}
	return found, nil
}
