// Copyright 2018 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// NOTE: The original code was modified to serve the needs of this project.

package leader

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// maxBackoffInterval defines the maximum amount of time to wait between
// attempts to become the leader.
const maxBackoffInterval = time.Second * 16

// Become ensures that the current pod is the leader within its namespace. If
// run outside a cluster, it will skip leader election and return nil. It
// continuously tries to create a ConfigMap with the provided name and the
// current pod set as the owner reference. Only one can exist at a time with
// the same name, so the pod that successfully creates the ConfigMap is the
// leader. Upon termination of that pod, the garbage collector will delete the
// ConfigMap, enabling a different pod to become the leader.
func Become(ctx context.Context, ns string, lockName string) error {
	glog.V(1).Info("Trying to become the leader.")

	if ns == "" {
		return fmt.Errorf("Namespace unset.")
	}

	config, err := config.GetConfig()
	if err != nil {
		return err
	}

	client, err := crclient.New(config, crclient.Options{})
	if err != nil {
		return err
	}

	owner, err := myOwnerRef(ctx, client, ns)
	if err != nil {
		return err
	}

	// check for existing lock from this pod, in case we got restarted
	existing := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
	}
	key := crclient.ObjectKey{Namespace: ns, Name: lockName}
	err = client.Get(ctx, key, existing)

	switch {
	case err == nil:
		for _, existingOwner := range existing.GetOwnerReferences() {
			if existingOwner.Name == owner.Name {
				glog.V(1).Info("Found existing lock with my name. I was likely restarted.")
				glog.V(1).Info("Continuing as the leader.")
				return nil
			} else {
				glog.V(1).Info("Found existing lock", "LockOwner", existingOwner.Name)
			}
		}
	case apierrors.IsNotFound(err):
		glog.V(1).Info("No pre-existing lock was found.")
	default:
		glog.Error(err, "Unknown error trying to get ConfigMap")
		return err
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            lockName,
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{*owner},
		},
	}

	// try to create a lock
	backoff := time.Second
	for {
		err := client.Create(ctx, cm)
		switch {
		case err == nil:
			glog.V(1).Info("Became the leader.")
			return nil
		case apierrors.IsAlreadyExists(err):
			glog.V(1).Info("Not the leader. Waiting.")
			select {
			case <-time.After(wait.Jitter(backoff, .2)):
				if backoff < maxBackoffInterval {
					backoff *= 2
				}
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			glog.Error(err, "Unknown error creating ConfigMap")
			return err
		}
	}
}

// getPod returns a Pod object that corresponds to the pod in which the code
// is currently running.
// It expects the environment variable POD_NAME to be set by the downwards API.
func getPod(ctx context.Context, client crclient.Client, ns string) (*corev1.Pod, error) {
	const PodNameEnvVar = "POD_NAME"

	podName := os.Getenv(PodNameEnvVar)
	if podName == "" {
		return nil, fmt.Errorf("Required env %s not set, please configure downward API", PodNameEnvVar)
	}

	glog.V(2).Info("Found podname", "Pod.Name", podName)

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
	}
	key := crclient.ObjectKey{Namespace: ns, Name: podName}
	err := client.Get(ctx, key, pod)
	if err != nil {
		glog.Error(err, "Failed to get Pod", "Pod.Namespace", ns, "Pod.Name", podName)
		return nil, err
	}

	glog.V(2).Info("Found Pod", "Pod.Namespace", ns, "Pod.Name", pod.Name)

	return pod, nil
}

// myOwnerRef returns an OwnerReference that corresponds to the pod in which
// this code is currently running.
// It expects the environment variable POD_NAME to be set by the downwards API
func myOwnerRef(ctx context.Context, client crclient.Client, ns string) (*metav1.OwnerReference, error) {
	myPod, err := getPod(ctx, client, ns)
	if err != nil {
		return nil, err
	}

	owner := &metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       myPod.ObjectMeta.Name,
		UID:        myPod.ObjectMeta.UID,
	}
	return owner, nil
}
