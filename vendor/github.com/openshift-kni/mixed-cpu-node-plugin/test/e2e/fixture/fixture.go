/*
 * Copyright 2023 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package fixture

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/openshift-kni/mixed-cpu-node-plugin/internal/wait"
	securityv1 "github.com/openshift/api/security/v1"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

var fxt Fixture

type Fixture struct {
	Ctx    context.Context
	Cli    client.Client
	K8SCli *kubernetes.Clientset
	NS     *corev1.Namespace
}

func New() *Fixture {
	fxt.Ctx = context.Background()

	if err := initClient(); err != nil {
		klog.Exit(err.Error())
	}
	if err := initK8SClient(); err != nil {
		klog.Exit(err.Error())
	}
	return &fxt
}

func initClient() error {
	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}

	if err = machineconfigv1.AddToScheme(scheme.Scheme); err != nil {
		return err
	}

	if err = securityv1.AddToScheme(scheme.Scheme); err != nil {
		return err
	}

	fxt.Cli, err = client.New(cfg, client.Options{})
	return err
}

func initK8SClient() error {
	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}
	fxt.K8SCli, err = kubernetes.NewForConfig(cfg)
	return err
}

func (fxt *Fixture) CreateNamespace(prefix string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix,
			Labels: map[string]string{
				"security.openshift.io/scc.podSecurityLabelSync": "false",
				"pod-security.kubernetes.io/audit":               "privileged",
				"pod-security.kubernetes.io/enforce":             "privileged",
				"pod-security.kubernetes.io/warn":                "privileged",
			},
		},
	}
	err := fxt.Cli.Create(context.TODO(), ns)
	if err != nil {
		return ns, fmt.Errorf("failed to create namespace %s; %w", ns.Name, err)
	}
	fxt.NS = ns
	return ns, nil
}

func (fxt *Fixture) DeleteNamespace(ns *corev1.Namespace) error {
	err := fxt.Cli.Delete(context.TODO(), ns)
	if err != nil {
		return fmt.Errorf("failed deleting namespace %q; %w", ns.Name, err)
	}
	return wait.ForNSDeletion(context.TODO(), fxt.Cli, client.ObjectKeyFromObject(ns))
}
