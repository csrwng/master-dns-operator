package masterdns

import (
	log "github.com/sirupsen/logrus"

	kubeclient "k8s.io/client-go/kubernetes"

	"github.com/openshift/library-go/pkg/operator/events"
)

func setupEventRecorder(kubeClient kubeclient.Interface) events.Recorder {
	controllerRef, err := events.GetControllerReferenceForCurrentPod(kubeClient, operatorNamespace, nil)
	if err != nil {
		log.WithError(err).Warning("Cannot determine pod name for event recorder. Using logger.")
		return &logRecorder{}
	}

	eventsClient := kubeClient.CoreV1().Events(controllerRef.Namespace)
	return events.NewRecorder(eventsClient, "openshift-master-dns-operator", controllerRef)
}

type logRecorder struct{}

func (logRecorder) Event(reason, message string) {
	log.WithField("reason", reason).Info(message)
}

func (logRecorder) Eventf(reason, messageFmt string, args ...interface{}) {
	log.WithField("reason", reason).Infof(messageFmt, args...)
}

func (logRecorder) Warning(reason, message string) {
	log.WithField("reason", reason).Warning(message)
}

func (logRecorder) Warningf(reason, messageFmt string, args ...interface{}) {
	log.WithField("reason", reason).Warningf(messageFmt, args...)
}
