package events

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetEventsForObject(cli client.Client, namespace, name, uid string) (corev1.EventList, error) {
	eventList := corev1.EventList{}
	match := client.MatchingFields{
		"involvedObject.name": name,
		"involvedObject.uid":  uid,
	}
	err := cli.List(context.TODO(), &eventList, &client.ListOptions{Namespace: namespace}, match)
	return eventList, err
}
