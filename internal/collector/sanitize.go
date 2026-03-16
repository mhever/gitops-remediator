package collector

import corev1 "k8s.io/api/core/v1"

// sanitize returns a deep copy of the pod with all env var values replaced
// by "[REDACTED]". Keys are preserved. Applied before any external transmission.
func sanitize(pod *corev1.Pod) *corev1.Pod {
	podCopy := pod.DeepCopy()
	redactEnvVars(podCopy.Spec.InitContainers)
	redactEnvVars(podCopy.Spec.Containers)
	return podCopy
}

// redactEnvVars replaces all env var values and ValueFrom references with
// "[REDACTED]" and nil respectively for each container in the slice.
func redactEnvVars(containers []corev1.Container) {
	for i := range containers {
		for j := range containers[i].Env {
			containers[i].Env[j].Value = "[REDACTED]"
			containers[i].Env[j].ValueFrom = nil
		}
	}
}
