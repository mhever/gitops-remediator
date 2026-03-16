package collector

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/mhever/gitops-remediator/internal/watcher"
)

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
