package watcher

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// classifyPod inspects a Pod's container statuses and returns FailureEvents
// for any monitored failure conditions found. Returns nil if none.
func classifyPod(pod *corev1.Pod) []FailureEvent {
	var events []FailureEvent

	for _, cs := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			events = append(events, FailureEvent{
				Namespace:     pod.Namespace,
				PodName:       pod.Name,
				ContainerName: cs.Name,
				FailureType:   FailureTypeOOMKilled,
				RawReason:     cs.State.Terminated.Reason,
				Timestamp:     time.Now(),
			})
			continue
		}
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff":
				events = append(events, FailureEvent{
					Namespace:     pod.Namespace,
					PodName:       pod.Name,
					ContainerName: cs.Name,
					FailureType:   FailureTypeCrashLoopBackOff,
					RawReason:     cs.State.Waiting.Reason,
					Timestamp:     time.Now(),
				})
			case "ImagePullBackOff", "ErrImagePull":
				events = append(events, FailureEvent{
					Namespace:     pod.Namespace,
					PodName:       pod.Name,
					ContainerName: cs.Name,
					FailureType:   FailureTypeImagePullBackOff,
					RawReason:     cs.State.Waiting.Reason,
					Timestamp:     time.Now(),
				})
			}
		}
	}

	return events
}

// classifyEvent inspects a Kubernetes Warning Event and returns a FailureEvent
// if it represents a monitored failure. Returns nil otherwise.
//
// Disambiguation rules:
//   - Reason "OOMKilling" → OOMKilled (unambiguous)
//   - Reason "BackOff" + message contains "pulling image" → ImagePullBackOff
//   - Reason "BackOff" + message contains "restarting failed container" → CrashLoopBackOff
//   - Reason "BackOff" + neither → skip (nil)
//   - Reason "Failed" + message contains "pull" or "pulling" → ImagePullBackOff
//   - Reason "Failed" + no image-related content → skip (nil)
func classifyEvent(event *corev1.Event) *FailureEvent {
	if event.Type != corev1.EventTypeWarning {
		return nil
	}

	msg := strings.ToLower(event.Message)

	var ft FailureType
	switch event.Reason {
	case "OOMKilling":
		ft = FailureTypeOOMKilled
	case "BackOff":
		if strings.Contains(msg, "pulling image") {
			ft = FailureTypeImagePullBackOff
		} else if strings.Contains(msg, "restarting failed container") {
			ft = FailureTypeCrashLoopBackOff
		} else {
			return nil
		}
	case "Failed":
		if strings.Contains(msg, "pull") {
			ft = FailureTypeImagePullBackOff
		} else {
			return nil
		}
	default:
		return nil
	}

	ts := event.LastTimestamp.Time
	if ts.IsZero() {
		ts = time.Now()
	}

	return &FailureEvent{
		Namespace:     event.Namespace,
		PodName:       event.InvolvedObject.Name,
		ContainerName: "",
		FailureType:   ft,
		RawReason:     event.Reason,
		Timestamp:     ts,
	}
}
