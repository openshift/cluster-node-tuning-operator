package controller

import (
	"context"
	"fmt"

	tunedv1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1alpha1"

	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"

	"github.com/operator-framework/operator-sdk/pkg/sdk"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

func NewHandler() sdk.Handler {
	return &Handler{
		manifestFactory: manifests.NewFactory(),
	}
}

type Handler struct {
	manifestFactory *manifests.Factory
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	if event.Deleted {
		return nil
	}
	switch o := event.Object.(type) {
	case *tunedv1alpha1.Tuned:
		return h.syncTunedUpdate(o)
	}
	return nil
}

func (h *Handler) syncDaemonSet(tuned *tunedv1alpha1.Tuned, sa *corev1.ServiceAccount) (*appsv1.DaemonSet, error) {
	/* TODO: actual syncing, just generate it for the moment */
	requiredDS := h.generateDaemonSet(sa)
	return requiredDS, nil
}

func (h *Handler) syncTunedUpdate(tuned *tunedv1alpha1.Tuned) error {
	ns, err := h.manifestFactory.TunedNamespace()
	if err != nil {
		return fmt.Errorf("couldn't build tuned namespace: %v", err)
	}
	err = sdk.Create(ns)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned namespace: %v", err)
		} else {
			logrus.Infof("tuned namespace already exists")
		}
	}

	sa, err := h.manifestFactory.TunedServiceAccount()
	if err != nil {
		return fmt.Errorf("couldn't build tuned service account: %v", err)
	}
	err = sdk.Create(sa)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("couldn't create tuned service account: %v", err)
	}

	cr, err := h.manifestFactory.TunedClusterRole()
	if err != nil {
		return fmt.Errorf("couldn't build tuned cluster role: %v", err)
	}
	err = sdk.Create(cr)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("couldn't create tuned cluster role: %v", err)
	}

	crb, err := h.manifestFactory.TunedClusterRoleBinding()
	if err != nil {
		return fmt.Errorf("couldn't build tuned cluster role binding: %v", err)
	}
	err = sdk.Create(crb)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("couldn't create tuned cluster role binding: %v", err)
	}

	cmProfiles, err := h.manifestFactory.TunedConfigMapProfiles()
	if err != nil {
		return fmt.Errorf("couldn't build tuned profiles config map: %v", err)
	}
	err = sdk.Create(cmProfiles)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("couldn't create tuned profiles config map: %v", err)
	}

	cmRecommend, err := h.manifestFactory.TunedConfigMapRecommend()
	if err != nil {
		return fmt.Errorf("couldn't build tuned recommend config map: %v", err)
	}
	err = sdk.Create(cmRecommend)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("couldn't create tuned recommend config map: %v", err)
	}

	ds, err := h.syncDaemonSet(tuned, sa)
	if err != nil {
		return fmt.Errorf("couldn't build tuned daemonset: %v", err)
	}
	err = sdk.Create(ds)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned daemonset: %v", err)
		} else {
			logrus.Infof("tuned daemonset already exists")
		}
	}

	return nil
}
