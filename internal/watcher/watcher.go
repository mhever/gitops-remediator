package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// FailureType is an enum of the failure modes the remediator handles.
type FailureType string

const (
	FailureTypeOOMKilled        FailureType = "OOMKilled"
	FailureTypeCrashLoopBackOff FailureType = "CrashLoopBackOff"
	FailureTypeImagePullBackOff FailureType = "ImagePullBackOff"
)

// FailureEvent is emitted by the Watcher when a monitored failure is detected.
type FailureEvent struct {
	Namespace     string
	PodName       string
	ContainerName string
	FailureType   FailureType
	RawReason     string
	Timestamp     time.Time
}

// Watcher watches a Kubernetes namespace for failure events.
type Watcher interface {
	// Run starts the watcher. It blocks until ctx is cancelled.
	Run(ctx context.Context) error
}

// NoopWatcher satisfies Watcher without doing anything.
// Used during Phase 0 scaffold and as a test double.
type NoopWatcher struct{}

// Compile-time interface check.
var _ Watcher = (*NoopWatcher)(nil)

func (n *NoopWatcher) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// K8sWatcher watches a Kubernetes namespace using SharedInformers and emits
// FailureEvents for detected pod failure conditions.
type K8sWatcher struct {
	client    kubernetes.Interface
	namespace string
	events    chan<- FailureEvent
	logger    *slog.Logger
	dedup     *deduplicator
}

// Compile-time interface check.
var _ Watcher = (*K8sWatcher)(nil)

// NewK8sWatcher creates a new K8sWatcher. The events channel must be
// buffered to avoid blocking the informer callback goroutine.
func NewK8sWatcher(client kubernetes.Interface, namespace string, events chan<- FailureEvent, logger *slog.Logger) *K8sWatcher {
	return &K8sWatcher{
		client:    client,
		namespace: namespace,
		events:    events,
		logger:    logger,
		dedup:     newDeduplicator(),
	}
}

// Run starts the SharedInformers for Pods and k8s Events, waits for cache
// sync, and blocks until ctx is cancelled. Returns ctx.Err() on shutdown.
func (w *K8sWatcher) Run(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		w.client,
		30*time.Second,
		informers.WithNamespace(w.namespace),
	)

	podInformer := factory.Core().V1().Pods().Informer()
	if _, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handlePodUpdate(nil, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			w.handlePodUpdate(oldObj, newObj)
		},
	}); err != nil {
		w.logger.Error("failed to register pod event handler", "error", err)
		return err
	}

	eventInformer := factory.Core().V1().Events().Informer()
	if _, err := eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handleEventAdd(obj)
		},
	}); err != nil {
		w.logger.Error("failed to register event handler", "error", err)
		return err
	}

	factory.Start(ctx.Done())
	synced := factory.WaitForCacheSync(ctx.Done())
	for informerType, ok := range synced {
		if !ok {
			w.logger.Warn("informer cache sync failed", "type", fmt.Sprintf("%T", informerType))
		}
	}

	<-ctx.Done()
	return ctx.Err()
}

// handlePodUpdate processes pod add/update events and emits FailureEvents.
func (w *K8sWatcher) handlePodUpdate(_, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		w.logger.Warn("handlePodUpdate: unexpected object type", "type", newObj)
		return
	}

	events := classifyPod(pod)
	for _, e := range events {
		if w.dedup.isDuplicate(e) {
			continue
		}
		select {
		case w.events <- e:
		default:
			w.logger.Warn("events channel full, dropping failure event",
				"pod", e.PodName,
				"type", e.FailureType,
			)
		}
	}
}

// handleEventAdd processes k8s Event add events and emits FailureEvents.
func (w *K8sWatcher) handleEventAdd(obj interface{}) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		w.logger.Warn("handleEventAdd: unexpected object type", "type", obj)
		return
	}

	fe := classifyEvent(event)
	if fe == nil {
		return
	}

	if w.dedup.isDuplicate(*fe) {
		return
	}

	select {
	case w.events <- *fe:
	default:
		w.logger.Warn("events channel full, dropping failure event",
			"pod", fe.PodName,
			"type", fe.FailureType,
		)
	}
}
