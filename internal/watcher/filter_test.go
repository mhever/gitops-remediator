package watcher

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClassifyPod(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		wantTypes      []FailureType
		wantContainers []string
		wantLen        int
	}{
		{
			name: "OOMKilled terminated container",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns1"},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
							},
						},
					},
				},
			},
			wantLen:        1,
			wantTypes:      []FailureType{FailureTypeOOMKilled},
			wantContainers: []string{"app"},
		},
		{
			name: "CrashLoopBackOff waiting container",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns1"},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app",
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
							},
						},
					},
				},
			},
			wantLen:        1,
			wantTypes:      []FailureType{FailureTypeCrashLoopBackOff},
			wantContainers: []string{"app"},
		},
		{
			name: "ImagePullBackOff waiting container",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p3", Namespace: "ns1"},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app",
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
							},
						},
					},
				},
			},
			wantLen:        1,
			wantTypes:      []FailureType{FailureTypeImagePullBackOff},
			wantContainers: []string{"app"},
		},
		{
			name: "ErrImagePull waiting container",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p4", Namespace: "ns1"},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app",
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"},
							},
						},
					},
				},
			},
			wantLen:        1,
			wantTypes:      []FailureType{FailureTypeImagePullBackOff},
			wantContainers: []string{"app"},
		},
		{
			name: "no failure status",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p5", Namespace: "ns1"},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app",
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{},
							},
						},
					},
				},
			},
			wantLen: 0,
		},
		{
			name: "multiple failing containers",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p6", Namespace: "ns1"},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app1",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
							},
						},
						{
							Name: "app2",
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
							},
						},
					},
				},
			},
			wantLen:        2,
			wantTypes:      []FailureType{FailureTypeOOMKilled, FailureTypeCrashLoopBackOff},
			wantContainers: []string{"app1", "app2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPod(tc.pod)
			if len(got) != tc.wantLen {
				t.Fatalf("classifyPod() returned %d events, want %d", len(got), tc.wantLen)
			}
			for i, e := range got {
				if e.FailureType != tc.wantTypes[i] {
					t.Errorf("event[%d].FailureType = %q, want %q", i, e.FailureType, tc.wantTypes[i])
				}
				if e.ContainerName != tc.wantContainers[i] {
					t.Errorf("event[%d].ContainerName = %q, want %q", i, e.ContainerName, tc.wantContainers[i])
				}
				if e.Namespace != tc.pod.Namespace {
					t.Errorf("event[%d].Namespace = %q, want %q", i, e.Namespace, tc.pod.Namespace)
				}
				if e.PodName != tc.pod.Name {
					t.Errorf("event[%d].PodName = %q, want %q", i, e.PodName, tc.pod.Name)
				}
			}
		})
	}
}

func TestClassifyEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *corev1.Event
		wantNil  bool
		wantType FailureType
		wantPod  string
	}{
		{
			name: "Warning OOMKilling",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeWarning,
				Reason:         "OOMKilling",
				InvolvedObject: corev1.ObjectReference{Name: "pod-abc"},
			},
			wantType: FailureTypeOOMKilled,
			wantPod:  "pod-abc",
		},
		{
			name: "BackOff with image-pull message → ImagePullBackOff",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeWarning,
				Reason:         "BackOff",
				Message:        "Back-off pulling image \"registry.example.com/app:bad\"",
				InvolvedObject: corev1.ObjectReference{Name: "pod-def"},
			},
			wantType: FailureTypeImagePullBackOff,
			wantPod:  "pod-def",
		},
		{
			name: "BackOff with restart message → CrashLoopBackOff",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeWarning,
				Reason:         "BackOff",
				Message:        "Back-off restarting failed container app in pod pod-def_ns1",
				InvolvedObject: corev1.ObjectReference{Name: "pod-def"},
			},
			wantType: FailureTypeCrashLoopBackOff,
			wantPod:  "pod-def",
		},
		{
			name: "BackOff with unrelated message → nil",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeWarning,
				Reason:         "BackOff",
				Message:        "some unrelated backoff message",
				InvolvedObject: corev1.ObjectReference{Name: "pod-def"},
			},
			wantNil: true,
		},
		{
			name: "Failed with pull message → ImagePullBackOff",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeWarning,
				Reason:         "Failed",
				Message:        "Failed to pull image \"registry.example.com/app:missing\": rpc error",
				InvolvedObject: corev1.ObjectReference{Name: "pod-ghi"},
			},
			wantType: FailureTypeImagePullBackOff,
			wantPod:  "pod-ghi",
		},
		{
			name: "Failed with unrelated message → nil",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeWarning,
				Reason:         "Failed",
				Message:        "Error: failed to create containerd task: failed to mount /var/lib/kubelet/pods/...",
				InvolvedObject: corev1.ObjectReference{Name: "pod-ghi"},
			},
			wantNil: true,
		},
		{
			name: "Normal event not Warning",
			event: &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1"},
				Type:           corev1.EventTypeNormal,
				Reason:         "OOMKilling",
				InvolvedObject: corev1.ObjectReference{Name: "pod-xyz"},
			},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyEvent(tc.event)
			if tc.wantNil {
				if got != nil {
					t.Errorf("classifyEvent() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("classifyEvent() = nil, want non-nil")
			}
			if got.FailureType != tc.wantType {
				t.Errorf("FailureType = %q, want %q", got.FailureType, tc.wantType)
			}
			if got.PodName != tc.wantPod {
				t.Errorf("PodName = %q, want %q", got.PodName, tc.wantPod)
			}
			if got.RawReason != tc.event.Reason {
				t.Errorf("RawReason = %q, want %q", got.RawReason, tc.event.Reason)
			}
		})
	}
}
