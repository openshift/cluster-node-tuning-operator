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

package operator

import (
	"context"
	"fmt"
	"os"
	"time"

	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metaapi "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

// maxBackoffInterval defines the maximum amount of time to wait between
// attempts to become the leader.
const maxBackoffInterval = time.Second * 16

// becomeLeader ensures that the current pod is the leader within its namespace.
// If run outside a cluster, it will skip leader election and return nil. It
// continuously tries to create a ConfigMap with the provided name and the
// current pod set as the owner reference. Only one can exist at a time with
// the same name, so the pod that successfully creates the ConfigMap is the
// leader. Upon termination of that pod, the garbage collector will delete the
// ConfigMap, enabling a different pod to become the leader.
func (c *Controller) becomeLeader(ns string, lockName string) error {
	klog.V(1).Info("Trying to become the leader.")

	if !isInCluster() {
		klog.V(1).Infof("Not running InCluster, skipping leader election.")
		return nil
	}

	if ns == "" {
		return fmt.Errorf("Namespace unset.")
	}

	owner, err := c.myOwnerRef(ns)
	if err != nil {
		return err
	}

	existing, err := c.clients.Core.ConfigMaps(ns).Get(context.TODO(), lockName, metaapi.GetOptions{})

	switch {
	case err == nil:
		for _, existingOwner := range existing.GetOwnerReferences() {
			if existingOwner.Name == owner.Name {
				klog.V(1).Info("Found existing lock with my name. I was likely restarted.")
				klog.V(1).Info("Continuing as the leader.")
				return nil
			} else {
				klog.V(1).Infof("Found existing lock %q", existingOwner.Name)
			}
		}
	case apierrors.IsNotFound(err):
		klog.V(1).Info("No pre-existing lock was found.")
	default:
		klog.Error(err, "Unknown error trying to get ConfigMap")
		return err
	}

	cm := &coreapi.ConfigMap{
		TypeMeta: metaapi.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metaapi.ObjectMeta{
			Name:            lockName,
			Namespace:       ns,
			OwnerReferences: []metaapi.OwnerReference{*owner},
		},
	}

	// try to create a lock
	backoff := time.Second
	for {
		_, err := c.clients.Core.ConfigMaps(ns).Create(context.TODO(), cm, metaapi.CreateOptions{})

		switch {
		case err == nil:
			klog.V(1).Info("Became the leader.")
			return nil
		case apierrors.IsAlreadyExists(err):
			klog.V(1).Info("Not the leader. Waiting.")
			select {
			case <-time.After(wait.Jitter(backoff, .2)):
				if backoff < maxBackoffInterval {
					backoff *= 2
				}
				continue
			}
		default:
			klog.Error(err, "Unknown error creating ConfigMap")
			return err
		}
	}
}

// getPod returns a Pod object that corresponds to the pod in which the code
// is currently running.
// It expects the environment variable POD_NAME to be set by the downwards API.
func (c *Controller) getPod(ns string) (*coreapi.Pod, error) {
	const PodNameEnvVar = "POD_NAME"

	podName := os.Getenv(PodNameEnvVar)
	if podName == "" {
		return nil, fmt.Errorf("Required env %s not set, please configure downward API", PodNameEnvVar)
	}

	klog.V(2).Infof("Getting Pod %s", podName)

	pod, err := c.clients.Core.Pods(ns).Get(context.TODO(), podName, metaapi.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get Pod %s/%s", ns, podName)
		return nil, err
	}

	klog.V(2).Infof("Found Pod %s/%s", ns, pod.Name)

	return pod, nil
}

// myOwnerRef returns an OwnerReference that corresponds to the pod in which
// this code is currently running.
// It expects the environment variable POD_NAME to be set by the downwards API
func (c *Controller) myOwnerRef(ns string) (*metaapi.OwnerReference, error) {
	myPod, err := c.getPod(ns)
	if err != nil {
		return nil, err
	}

	owner := &metaapi.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       myPod.ObjectMeta.Name,
		UID:        myPod.ObjectMeta.UID,
	}
	return owner, nil
}

func isInCluster() bool {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if len(host) == 0 || len(port) == 0 {
		return false
	}

	// KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT are defined;
	// for the purposes of leader election assume we're running InCluster
	return true
}
