package patcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhever/gitops-remediator/internal/diagnostician"
	"github.com/mhever/gitops-remediator/internal/watcher"
)

func TestNoopPatcher_ApplyReturnsNil(t *testing.T) {
	p := &NoopPatcher{}
	diag := diagnostician.Diagnosis{
		FailureType: "OOMKilled",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		Namespace: "default",
		PodName:   "test-pod",
	}

	result, err := p.Apply(context.Background(), "/tmp", diag, event)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if result == nil {
		t.Errorf("expected non-nil result, got nil")
	}
}

// copyFixture copies a testdata fixture into a temp dir, returning the dir and dst path.
func copyFixture(t *testing.T, srcPath string) (string, string) {
	t.Helper()
	content, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("copyFixture: read %s: %v", srcPath, err)
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, filepath.Base(srcPath))
	if err := os.WriteFile(dst, content, 0644); err != nil {
		t.Fatalf("copyFixture: write %s: %v", dst, err)
	}
	return dir, dst
}

func TestManifestPatcher_ApplyMemoryLimit(t *testing.T) {
	dir, _ := copyFixture(t, "testdata/deployment-oom.yaml")

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		FailureType: "OOMKilled",
		PatchType:   "memory_limit",
		PatchValue:  "256Mi",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		PodName:   "sample-app-abc12-xyz99",
		Namespace: "remediator-test",
	}

	result, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	newStr := string(result.NewContent)
	if !strings.Contains(newStr, "memory: 256Mi") {
		t.Errorf("expected new content to contain 'memory: 256Mi', got:\n%s", newStr)
	}
	if strings.Contains(newStr, "memory: 128Mi") {
		t.Errorf("expected new content to NOT contain old 'memory: 128Mi', got:\n%s", newStr)
	}
}

func TestManifestPatcher_ApplyEnvVar(t *testing.T) {
	dir, _ := copyFixture(t, "testdata/deployment-crashloop.yaml")

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		FailureType: "CrashLoopBackOff",
		PatchType:   "env_var",
		PatchValue:  "DATABASE_URL=postgres://newhost:5432/mydb",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		PodName:   "sample-app-abc12-xyz99",
		Namespace: "remediator-test",
	}

	result, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	newStr := string(result.NewContent)
	if !strings.Contains(newStr, "postgres://newhost:5432/mydb") {
		t.Errorf("expected new content to contain new DB URL, got:\n%s", newStr)
	}
	if strings.Contains(newStr, "postgres://localhost:5432/mydb") {
		t.Errorf("expected new content to NOT contain old DB URL, got:\n%s", newStr)
	}
}

func TestManifestPatcher_ApplyImageTag(t *testing.T) {
	dir, _ := copyFixture(t, "testdata/deployment-imagepull.yaml")

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		FailureType: "ImagePullBackOff",
		PatchType:   "image_tag",
		PatchValue:  "1.26.0",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		PodName:       "sample-app-abc12-xyz99",
		Namespace:     "remediator-test",
		ContainerName: "app",
	}

	result, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	newStr := string(result.NewContent)
	if !strings.Contains(newStr, "image: nginx:1.26.0") {
		t.Errorf("expected new content to contain 'image: nginx:1.26.0', got:\n%s", newStr)
	}
}

func TestManifestPatcher_ValidatesYAML(t *testing.T) {
	dir, _ := copyFixture(t, "testdata/deployment-oom.yaml")

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		PatchType:  "memory_limit",
		PatchValue: "512Mi",
		Remediable: true,
	}
	event := watcher.FailureEvent{
		PodName:   "sample-app-abc12-xyz99",
		Namespace: "remediator-test",
	}

	_, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Errorf("expected no error for valid YAML patch, got: %v", err)
	}
}

func TestManifestPatcher_NoManifestFound(t *testing.T) {
	dir := t.TempDir()

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		PatchType:  "memory_limit",
		PatchValue: "256Mi",
		Remediable: true,
	}
	event := watcher.FailureEvent{
		PodName:   "sample-app-abc12-xyz99",
		Namespace: "remediator-test",
	}

	_, err := p.Apply(context.Background(), dir, diag, event)
	if err == nil {
		t.Error("expected error when no manifest found, got nil")
	}
}

func TestDeploymentName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sample-app-7d9b4c5f6-xkplt", "sample-app"},
		{"web-server-abc12-xyz99", "web-server"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := deploymentName(tt.input)
			if got != tt.want {
				t.Errorf("deploymentName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestApplyMemoryLimit_ResourcesEmptyInline(t *testing.T) {
	// Reproduces the real sample-app case: base deployment has `resources: {}`
	// because limits are not defined in the Kustomize base.
	input := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
spec:
  template:
    spec:
      containers:
      - name: sample-app
        image: ghcr.io/mhever/sample-app:sha-abc123
        resources: {}
`)
	result, err := applyMemoryLimit(input, "sample-app", "256Mi")
	if err != nil {
		t.Fatalf("applyMemoryLimit: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "memory: 256Mi") {
		t.Errorf("expected 'memory: 256Mi' in output, got:\n%s", out)
	}
	if strings.Contains(out, "resources: {}") {
		t.Errorf("expected 'resources: {}' to be expanded, got:\n%s", out)
	}
}

func TestApplyMemoryLimit_ResourcesBlockNoLimits(t *testing.T) {
	// resources: block exists with requests but no limits: — insert limits block.
	input := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
spec:
  template:
    spec:
      containers:
      - name: app
        resources:
          requests:
            memory: 64Mi
            cpu: 100m
`)
	result, err := applyMemoryLimit(input, "app", "256Mi")
	if err != nil {
		t.Fatalf("applyMemoryLimit: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "limits:") {
		t.Errorf("expected 'limits:' to be inserted, got:\n%s", out)
	}
	if !strings.Contains(out, "memory: 256Mi") {
		t.Errorf("expected 'memory: 256Mi' in output, got:\n%s", out)
	}
	// requests must be preserved
	if !strings.Contains(out, "memory: 64Mi") {
		t.Errorf("expected requests 'memory: 64Mi' preserved, got:\n%s", out)
	}
}

func TestApplyMemoryLimit_LimitsBlockNoMemory(t *testing.T) {
	// limits: block exists but only has cpu: — insert memory: line.
	input := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
spec:
  template:
    spec:
      containers:
      - name: app
        resources:
          limits:
            cpu: 200m
`)
	result, err := applyMemoryLimit(input, "app", "256Mi")
	if err != nil {
		t.Fatalf("applyMemoryLimit: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "memory: 256Mi") {
		t.Errorf("expected 'memory: 256Mi' inserted, got:\n%s", out)
	}
	if !strings.Contains(out, "cpu: 200m") {
		t.Errorf("expected 'cpu: 200m' preserved, got:\n%s", out)
	}
}

func TestApplyMemoryLimit_NoResourcesBlock(t *testing.T) {
	// Reproduces the real sample-app base deployment: no resources block at all.
	// applyMemoryLimit must insert a complete resources: limits: memory: block.
	input := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
spec:
  template:
    spec:
      containers:
      - name: sample-app
        image: ghcr.io/mhever/sample-app:sha-abc123
        ports:
        - containerPort: 8080
`)
	result, err := applyMemoryLimit(input, "sample-app", "256Mi")
	if err != nil {
		t.Fatalf("applyMemoryLimit: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "resources:") {
		t.Errorf("expected 'resources:' inserted, got:\n%s", out)
	}
	if !strings.Contains(out, "limits:") {
		t.Errorf("expected 'limits:' inserted, got:\n%s", out)
	}
	if !strings.Contains(out, "memory: 256Mi") {
		t.Errorf("expected 'memory: 256Mi' inserted, got:\n%s", out)
	}
	// Existing fields must be preserved.
	if !strings.Contains(out, "containerPort: 8080") {
		t.Errorf("expected 'containerPort: 8080' preserved, got:\n%s", out)
	}
}

func TestApplyMemoryLimit_NoResourcesBlock_EmptyContainerName(t *testing.T) {
	// Same as above but with empty containerName — must patch the first container.
	input := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
spec:
  template:
    spec:
      containers:
      - name: sample-app
        image: ghcr.io/mhever/sample-app:sha-abc123
`)
	result, err := applyMemoryLimit(input, "", "256Mi")
	if err != nil {
		t.Fatalf("applyMemoryLimit: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "memory: 256Mi") {
		t.Errorf("expected 'memory: 256Mi' inserted, got:\n%s", out)
	}
}

func TestApplyMemoryLimit_WithExtraLimitsFields(t *testing.T) {
	// Verify applyMemoryLimit works when limits: block contains extra fields
	// like ephemeral-storage (regression for MAJOR #4).
	input := []byte(`apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: app
        resources:
          limits:
            cpu: 200m
            memory: 128Mi
            ephemeral-storage: 1Gi
`)
	result, err := applyMemoryLimit(input, "", "512Mi")
	if err != nil {
		t.Fatalf("applyMemoryLimit: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "memory: 512Mi") {
		t.Errorf("expected 'memory: 512Mi' in output, got:\n%s", out)
	}
	if strings.Contains(out, "memory: 128Mi") {
		t.Errorf("expected old 'memory: 128Mi' to be replaced, got:\n%s", out)
	}
	// Other fields must be preserved
	if !strings.Contains(out, "ephemeral-storage: 1Gi") {
		t.Errorf("expected 'ephemeral-storage: 1Gi' to be preserved, got:\n%s", out)
	}
}

func TestManifestPatcher_ApplyMemoryLimit_ContainerScoped(t *testing.T) {
	// Two-container fixture: only the named container's memory limit should be changed.
	dir := t.TempDir()
	fixture := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
spec:
  template:
    spec:
      containers:
      - name: sidecar
        resources:
          limits:
            memory: 64Mi
      - name: app
        resources:
          limits:
            memory: 128Mi
`)
	manifestPath := dir + "/deployment.yaml"
	if err := os.WriteFile(manifestPath, fixture, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		FailureType: "OOMKilled",
		PatchType:   "memory_limit",
		PatchValue:  "256Mi",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		PodName:       "sample-app-abc12-xyz99",
		Namespace:     "remediator-test",
		ContainerName: "app",
	}

	result, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	newStr := string(result.NewContent)
	if !strings.Contains(newStr, "memory: 256Mi") {
		t.Errorf("expected 'memory: 256Mi' in output, got:\n%s", newStr)
	}
	// sidecar memory must be untouched
	if !strings.Contains(newStr, "memory: 64Mi") {
		t.Errorf("expected sidecar 'memory: 64Mi' to be preserved, got:\n%s", newStr)
	}
	// old app memory must be replaced
	if strings.Contains(newStr, "memory: 128Mi") {
		t.Errorf("expected old 'memory: 128Mi' to be replaced, got:\n%s", newStr)
	}
}

func TestApplyImageTag_ContainerScoped(t *testing.T) {
	// Verify applyImageTag patches only the correct container's image.
	input := []byte(`apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: sidecar
        image: envoy:1.0.0
      - name: app
        image: nginx:1.25.0
`)
	result, err := applyImageTag(input, "app", "1.26.0")
	if err != nil {
		t.Fatalf("applyImageTag: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "image: nginx:1.26.0") {
		t.Errorf("expected 'image: nginx:1.26.0' in output, got:\n%s", out)
	}
	// sidecar image must be untouched
	if !strings.Contains(out, "image: envoy:1.0.0") {
		t.Errorf("expected 'image: envoy:1.0.0' to be preserved, got:\n%s", out)
	}
}

func TestApplyImageTag_ContainerNotFound(t *testing.T) {
	input := []byte(`spec:
  containers:
  - name: app
    image: nginx:1.25.0
`)
	_, err := applyImageTag(input, "missing-container", "1.26.0")
	if err == nil {
		t.Error("expected error when container not found, got nil")
	}
}

func TestContainsDeploymentWithName_MatchesMetadataName(t *testing.T) {
	content := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: default
`
	if !containsDeploymentWithName(content, "my-app") {
		t.Error("expected true for matching deployment name")
	}
}

func TestContainsDeploymentWithName_NoFalsePositive(t *testing.T) {
	// "name: my-app" appears inside spec but not at metadata level after kind
	content := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: other-app
spec:
  selector:
    matchLabels:
      app: my-app
`
	// Should match "other-app" (metadata.name) but NOT "my-app" (label value)
	if containsDeploymentWithName(content, "my-app") {
		t.Error("expected false for name appearing only in labels, not metadata")
	}
	if !containsDeploymentWithName(content, "other-app") {
		t.Error("expected true for correct metadata name")
	}
}

func TestContainsDeploymentWithName_MultiDocServiceShouldNotMatch(t *testing.T) {
	// Multi-document YAML: first doc is Deployment with different name,
	// second doc is Service with the target name — must return false.
	content := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: other-app
---
apiVersion: v1
kind: Service
metadata:
  name: my-app
`
	if containsDeploymentWithName(content, "my-app") {
		t.Error("expected false: name appears under kind: Service, not Deployment/StatefulSet")
	}
}

func TestContainsDeploymentWithName_MultiDocSecondDocMatches(t *testing.T) {
	// Multi-document YAML with --- separator: target deployment is in the second doc.
	content := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: other-app
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
`
	if !containsDeploymentWithName(content, "my-app") {
		t.Error("expected true: target deployment found in second document")
	}
}

func TestManifestPatcher_ApplyImageTag_Kustomize(t *testing.T) {
	// Copy kustomization fixture into a temp dir as "kustomization.yaml" (no deployment YAML present).
	content, err := os.ReadFile("testdata/kustomization-imagepull.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "kustomization.yaml")
	if err := os.WriteFile(dst, content, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_ = dst

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		FailureType: "ImagePullBackOff",
		PatchType:   "image_tag",
		PatchValue:  "sha-8644a17",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		PodName:   "sample-app-abc12-xyz99",
		Namespace: "remediator-test",
	}

	result, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	newStr := string(result.NewContent)
	if !strings.Contains(newStr, "newTag: sha-8644a17") {
		t.Errorf("expected new content to contain 'newTag: sha-8644a17', got:\n%s", newStr)
	}
	if strings.Contains(newStr, "old-sha-abc123") {
		t.Errorf("expected new content to NOT contain 'old-sha-abc123', got:\n%s", newStr)
	}
	if !strings.HasSuffix(result.FilePath, "kustomization.yaml") {
		t.Errorf("expected FilePath to end with 'kustomization.yaml', got: %s", result.FilePath)
	}
}

func TestManifestPatcher_ApplyImageTag_FallbackToDeployment(t *testing.T) {
	// Use existing deployment-imagepull.yaml with no kustomization.yaml in the temp dir.
	dir, _ := copyFixture(t, "testdata/deployment-imagepull.yaml")

	p := NewManifestPatcher()
	diag := diagnostician.Diagnosis{
		FailureType: "ImagePullBackOff",
		PatchType:   "image_tag",
		PatchValue:  "1.26.0",
		Remediable:  true,
	}
	event := watcher.FailureEvent{
		PodName:       "sample-app-abc12-xyz99",
		Namespace:     "remediator-test",
		ContainerName: "app",
	}

	result, err := p.Apply(context.Background(), dir, diag, event)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	newStr := string(result.NewContent)
	if !strings.Contains(newStr, "image: nginx:1.26.0") {
		t.Errorf("expected new content to contain 'image: nginx:1.26.0', got:\n%s", newStr)
	}
}

func TestApplyKustomizationImageTag_UpdatesExistingTag(t *testing.T) {
	input := []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
images:
- name: sample-app
  newName: ghcr.io/mhever/sample-app
  newTag: old-sha
`)
	result, err := applyKustomizationImageTag(input, "sample-app", "sha-8644a17")
	if err != nil {
		t.Fatalf("applyKustomizationImageTag: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "newTag: sha-8644a17") {
		t.Errorf("expected 'newTag: sha-8644a17' in output, got:\n%s", out)
	}
	if strings.Contains(out, "old-sha") {
		t.Errorf("expected 'old-sha' to be replaced, got:\n%s", out)
	}
}

func TestApplyKustomizationImageTag_InsertsNewTag(t *testing.T) {
	// Entry has name: and newName: but no newTag:
	input := []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
images:
- name: sample-app
  newName: ghcr.io/mhever/sample-app
`)
	result, err := applyKustomizationImageTag(input, "sample-app", "sha-8644a17")
	if err != nil {
		t.Fatalf("applyKustomizationImageTag: %v", err)
	}
	out := string(result)
	if !strings.Contains(out, "newTag: sha-8644a17") {
		t.Errorf("expected inserted 'newTag: sha-8644a17' in output, got:\n%s", out)
	}
	// Verify the inserted line has correct indentation (2 spaces past the '-' position)
	// The '-' is at column 0, so fields should be at 2 spaces.
	if !strings.Contains(out, "  newTag: sha-8644a17") {
		t.Errorf("expected indented '  newTag: sha-8644a17' in output, got:\n%s", out)
	}
}

func TestContainsKustomizationImage(t *testing.T) {
	validKustomization := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
images:
- name: sample-app
  newName: ghcr.io/mhever/sample-app
  newTag: sha-abc
`
	// True case: matching name
	if !containsKustomizationImage(validKustomization, "sample-app") {
		t.Error("expected true for kustomization with matching image name")
	}

	// False case: different image name
	if containsKustomizationImage(validKustomization, "other-app") {
		t.Error("expected false for kustomization with different image name")
	}

	// False case: no images block
	noImages := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
- deployment.yaml
`
	if containsKustomizationImage(noImages, "sample-app") {
		t.Error("expected false for kustomization with no images block")
	}
}

func TestDeploymentName_StatefulSetPod(t *testing.T) {
	// StatefulSet pod names use <name>-<ordinal> pattern. Stripping two segments
	// is incorrect for ordinal-style names (see deploymentName doc comment).
	// Current behaviour: strips two segments, which is wrong for this case.
	// This test documents the existing behaviour as a known limitation.
	got := deploymentName("sample-app-0")
	// After stripping: "sample-app-0" -> strip last "-0" -> "sample-app" -> strip last "-app" -> "sample"
	// This is the current (documented-limitation) behaviour.
	want := "sample"
	if got != want {
		t.Errorf("deploymentName(%q) = %q, want %q (known limitation for StatefulSet pods)", "sample-app-0", got, want)
	}
}
