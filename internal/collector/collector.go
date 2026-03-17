package collector

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/mhever/gitops-remediator/internal/watcher"
)

// ErrPodGone is returned by Collect when the pod no longer exists.
// This is a normal race between event emission and collection.
var ErrPodGone = errors.New("pod no longer exists")

// DiagnosticBundle is the assembled context sent to the Diagnostician.
type DiagnosticBundle struct {
	// Content is a structured plain-text block (not JSON).
	Content string
}

// Collector assembles a DiagnosticBundle from a FailureEvent.
type Collector interface {
	Collect(ctx context.Context, event watcher.FailureEvent) (*DiagnosticBundle, error)
}

// NoopCollector satisfies Collector without doing anything.
type NoopCollector struct{}

var _ Collector = (*NoopCollector)(nil)

// Collect returns an empty DiagnosticBundle without making any API calls.
func (n *NoopCollector) Collect(ctx context.Context, event watcher.FailureEvent) (*DiagnosticBundle, error) {
	return &DiagnosticBundle{Content: ""}, nil
}

// K8sCollector implements Collector using the Kubernetes API.
type K8sCollector struct {
	client kubernetes.Interface
	logger *slog.Logger
}

// NewK8sCollector creates a new K8sCollector.
func NewK8sCollector(client kubernetes.Interface, logger *slog.Logger) *K8sCollector {
	return &K8sCollector{
		client: client,
		logger: logger,
	}
}

var _ Collector = (*K8sCollector)(nil)

// Collect assembles a DiagnosticBundle for the given FailureEvent.
func (c *K8sCollector) Collect(ctx context.Context, event watcher.FailureEvent) (*DiagnosticBundle, error) {
	ns := event.Namespace
	podName := event.PodName

	// 1. Fetch pod spec + status.
	pod, err := c.client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrPodGone, ns, podName)
		}
		return nil, fmt.Errorf("collector: get pod %s/%s: %w", ns, podName, err)
	}

	// Sanitize before rendering.
	sanitizedPod := sanitize(pod)

	var buf bytes.Buffer

	// === FAILURE EVENT ===
	fmt.Fprintf(&buf, "=== FAILURE EVENT ===\n")
	fmt.Fprintf(&buf, "Type: %s\n", event.FailureType)
	fmt.Fprintf(&buf, "Namespace: %s\n", event.Namespace)
	fmt.Fprintf(&buf, "Pod: %s\n", event.PodName)
	fmt.Fprintf(&buf, "Container: %s\n", event.ContainerName)
	fmt.Fprintf(&buf, "Timestamp: %s\n", event.Timestamp.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&buf, "\n")

	// === POD SPEC (sanitized) ===
	fmt.Fprintf(&buf, "=== POD SPEC (sanitized) ===\n")
	buf.WriteString(c.sectionPodSpec(sanitizedPod, podName))
	fmt.Fprintf(&buf, "\n")

	// === POD STATUS ===
	fmt.Fprintf(&buf, "=== POD STATUS ===\n")
	fmt.Fprintf(&buf, "Phase: %s\n", pod.Status.Phase)
	fmt.Fprintf(&buf, "Conditions:")
	if len(pod.Status.Conditions) == 0 {
		fmt.Fprintf(&buf, " []\n")
	} else {
		fmt.Fprintf(&buf, "\n")
		for _, cond := range pod.Status.Conditions {
			fmt.Fprintf(&buf, "  %s=%s\n", cond.Type, cond.Status)
		}
	}
	fmt.Fprintf(&buf, "ContainerStatuses:\n")
	for _, cs := range pod.Status.ContainerStatuses {
		stateStr := containerStateString(cs.State)
		fmt.Fprintf(&buf, "  - name: %s  ready: %v  restartCount: %d  state: %s\n",
			cs.Name, cs.Ready, cs.RestartCount, stateStr)
	}
	fmt.Fprintf(&buf, "\n")

	// === RESOURCE LIMITS ===
	fmt.Fprintf(&buf, "=== RESOURCE LIMITS ===\n")
	for _, container := range sanitizedPod.Spec.Containers {
		res := container.Resources
		cpuReq := res.Requests.Cpu().String()
		cpuLimit := res.Limits.Cpu().String()
		memReq := res.Requests.Memory().String()
		memLimit := res.Limits.Memory().String()
		fmt.Fprintf(&buf, "  %s: cpu_req=%s cpu_limit=%s mem_req=%s mem_limit=%s\n",
			container.Name, cpuReq, cpuLimit, memReq, memLimit)
	}
	fmt.Fprintf(&buf, "\n")

	// === PREVIOUS IMAGE ===
	prevTag := c.previousImage(ctx, pod, event.ContainerName)
	if prevTag != "" {
		containerLabel := event.ContainerName
		if containerLabel == "" {
			containerLabel = "unknown"
		}
		fmt.Fprintf(&buf, "=== PREVIOUS IMAGE ===\n")
		fmt.Fprintf(&buf, "Container: %s\n", containerLabel)
		fmt.Fprintf(&buf, "Tag: %s\n", prevTag)
		fmt.Fprintf(&buf, "Note: This was the last successfully running image tag before the current failure. It is provided as a rollback hint only — verify the tag still exists in the registry before using it.\n")
		fmt.Fprintf(&buf, "\n")
	}

	// === RECENT EVENTS (last 5) ===
	fmt.Fprintf(&buf, "=== RECENT EVENTS (last 5) ===\n")
	buf.WriteString(c.sectionEvents(ctx, ns, podName))
	fmt.Fprintf(&buf, "\n")

	// === CONTAINER LOGS (last 100 lines) ===
	fmt.Fprintf(&buf, "=== CONTAINER LOGS (last 100 lines) ===\n")

	containerNames := collectContainerNames(event.ContainerName, pod)
	for _, cname := range containerNames {
		fmt.Fprintf(&buf, "--- %s ---\n", cname)
		buf.WriteString(c.sectionContainerLogs(ctx, ns, podName, cname))
	}

	return &DiagnosticBundle{Content: buf.String()}, nil
}

// sectionPodSpec marshals the sanitised pod spec to YAML.
// Returns a placeholder string on error.
func (c *K8sCollector) sectionPodSpec(pod *corev1.Pod, podName string) string {
	specYAML, err := sigsyaml.Marshal(pod.Spec)
	if err != nil {
		c.logger.Warn("failed to marshal pod spec", "pod", podName, "error", err)
		return fmt.Sprintf("<marshal error: %v>\n", err)
	}
	return string(specYAML)
}

// sectionEvents fetches and formats the last 5 events for a pod.
// Returns a placeholder string on error.
func (c *K8sCollector) sectionEvents(ctx context.Context, ns, podName string) string {
	eventList, err := c.client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + podName,
	})
	if err != nil {
		c.logger.Warn("failed to list events", "pod", podName, "error", err)
		return fmt.Sprintf("  <error fetching events: %v>\n", err)
	}
	events := eventList.Items
	sort.Slice(events, func(i, j int) bool {
		return events[i].LastTimestamp.After(events[j].LastTimestamp.Time)
	})
	limit := 5
	if len(events) < limit {
		limit = len(events)
	}
	var sb strings.Builder
	for _, ev := range events[:limit] {
		ts := ev.LastTimestamp.UTC().Format("2006-01-02T15:04:05Z")
		fmt.Fprintf(&sb, "  %s  %s  %s  %s\n", ts, ev.Type, ev.Reason, ev.Message)
	}
	return sb.String()
}

// sectionContainerLogs fetches and returns the last 100 lines of logs for a container.
// Returns a placeholder string on error.
func (c *K8sCollector) sectionContainerLogs(ctx context.Context, ns, podName, cname string) string {
	logs, err := c.fetchLogs(ctx, ns, podName, cname)
	if err != nil {
		c.logger.Warn("failed to fetch logs", "pod", podName, "container", cname, "error", err)
		return fmt.Sprintf("<log fetch error: %v>\n", err)
	}
	return logs
}

// collectContainerNames returns a list of container names to fetch logs for.
// If containerName is non-empty, returns just that. Otherwise returns all
// containers in the pod spec.
func collectContainerNames(containerName string, pod *corev1.Pod) []string {
	if containerName != "" {
		return []string{containerName}
	}
	names := make([]string, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		names = append(names, c.Name)
	}
	return names
}

// fetchLogs retrieves the last 100 lines of logs for a container.
func (c *K8sCollector) fetchLogs(ctx context.Context, ns, podName, containerName string) (string, error) {
	tail := int64(100)
	req := c.client.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &tail,
		Container: containerName,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("stream logs: %w", err)
	}
	defer stream.Close()

	var sb strings.Builder
	scanner := bufio.NewScanner(stream)
	lineCount := 0
	for scanner.Scan() && lineCount < 100 {
		sb.WriteString(scanner.Text())
		sb.WriteByte('\n')
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		return sb.String(), fmt.Errorf("read logs: %w", err)
	}
	return sb.String(), nil
}

// previousImage attempts to find the image tag that was running before the
// current failing pod by walking pod → ReplicaSet → Deployment → old ReplicaSets.
// Returns empty string on any error or if no previous RS can be found.
func (c *K8sCollector) previousImage(ctx context.Context, pod *corev1.Pod, containerName string) string {
	ns := pod.Namespace

	// 1. Find the pod's owning ReplicaSet name.
	var rsName string
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			rsName = ref.Name
			break
		}
	}
	if rsName == "" {
		return ""
	}

	// 2. Fetch the current RS.
	currentRS, err := c.client.AppsV1().ReplicaSets(ns).Get(ctx, rsName, metav1.GetOptions{})
	if err != nil {
		return ""
	}

	// 3. Find the owning Deployment name.
	var deployName string
	for _, ref := range currentRS.OwnerReferences {
		if ref.Kind == "Deployment" {
			deployName = ref.Name
			break
		}
	}
	if deployName == "" {
		return ""
	}

	// 4. List all ReplicaSets in the namespace.
	rsList, err := c.client.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}

	// 5. Determine the failing image tag from the current pod spec so we can
	//    skip previous RSes that used the same bad tag.
	failingTag := ""
	for _, c := range pod.Spec.Containers {
		if containerName == "" || c.Name == containerName {
			if idx := strings.LastIndex(c.Image, ":"); idx >= 0 {
				failingTag = c.Image[idx+1:]
			}
			break
		}
	}

	// Collect all RSes owned by the same Deployment, excluding the current one,
	// then sort by deployment.kubernetes.io/revision annotation descending (highest
	// revision = most recently active). creationTimestamp is NOT used here because
	// kubectl rollbacks reuse an existing RS (incrementing its revision without
	// changing its creation time), so revision is the only reliable ordering key.
	var candidates []*appsv1.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if rs.Name == rsName {
			continue
		}
		for _, ref := range rs.OwnerReferences {
			if ref.Kind == "Deployment" && ref.Name == deployName {
				candidates = append(candidates, rs)
				break
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return rsRevision(candidates[i]) > rsRevision(candidates[j])
	})

	// Pick the most recent previous RS whose image tag differs from the
	// currently failing tag. This skips over RSes from earlier failed
	// deployments that share the same bad tag.
	var prevRS *appsv1.ReplicaSet
	for _, rs := range candidates {
		tag := rsImageTag(rs, containerName)
		if failingTag == "" || tag != failingTag {
			prevRS = rs
			break
		}
	}
	if prevRS == nil {
		return ""
	}

	// 6. Find the matching container image in the previous RS.
	containers := prevRS.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return ""
	}
	image := ""
	if containerName == "" {
		image = containers[0].Image
	} else {
		for _, c := range containers {
			if c.Name == containerName {
				image = c.Image
				break
			}
		}
	}
	if image == "" {
		return ""
	}

	// Extract the tag portion (after the last ':').
	if idx := strings.LastIndex(image, ":"); idx >= 0 {
		return image[idx+1:]
	}
	return image
}

// rsRevision returns the integer value of the deployment.kubernetes.io/revision
// annotation on a ReplicaSet. Returns 0 if the annotation is absent or unparseable.
// Kubernetes increments this counter on every rollout, including rollbacks that
// reuse an existing RS — making it a more reliable ordering key than creationTimestamp.
func rsRevision(rs *appsv1.ReplicaSet) int64 {
	if rs.Annotations == nil {
		return 0
	}
	v := rs.Annotations["deployment.kubernetes.io/revision"]
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// rsImageTag extracts the tag portion of the image for the named container in
// a ReplicaSet's pod template. If containerName is empty, the first container
// is used. Returns empty string if the container is not found or has no tag.
func rsImageTag(rs *appsv1.ReplicaSet, containerName string) string {
	containers := rs.Spec.Template.Spec.Containers
	for _, c := range containers {
		if containerName == "" || c.Name == containerName {
			if idx := strings.LastIndex(c.Image, ":"); idx >= 0 {
				return c.Image[idx+1:]
			}
			return c.Image
		}
	}
	return ""
}

// containerStateString formats a ContainerState as a human-readable string.
func containerStateString(state corev1.ContainerState) string {
	if state.Running != nil {
		return "Running"
	}
	if state.Waiting != nil {
		return fmt.Sprintf("Waiting(%s)", state.Waiting.Reason)
	}
	if state.Terminated != nil {
		return fmt.Sprintf("Terminated(%s)", state.Terminated.Reason)
	}
	return "Unknown"
}
