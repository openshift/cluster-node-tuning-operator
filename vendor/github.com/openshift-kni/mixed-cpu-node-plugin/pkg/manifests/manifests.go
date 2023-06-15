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

package manifests

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1 "github.com/openshift/api/security/v1"
)

//go:embed yamls
var dir embed.FS

type Manifests struct {
	NS   corev1.Namespace
	SA   corev1.ServiceAccount
	DS   appsv1.DaemonSet
	Role rbacv1.Role
	RB   rbacv1.RoleBinding
	SCC  securityv1.SecurityContextConstraints
}

func Get(sharedCPUs string, opts ...func(mf *Manifests)) (*Manifests, error) {
	mf := Manifests{}
	var fileToObject = map[string]metav1.Object{
		"serviceaccount.yaml":            &mf.SA,
		"daemonset.yaml":                 &mf.DS,
		"role.yaml":                      &mf.Role,
		"rolebinding.yaml":               &mf.RB,
		"securitycontextconstraint.yaml": &mf.SCC,
	}

	files, err := dir.ReadDir("yamls")
	if err != nil {
		return nil, fmt.Errorf("failed to read yamls directory: %w", err)
	}

	for _, f := range files {
		fullPath := filepath.Join("yamls", f.Name())
		data, err := dir.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", fullPath, err)
		}
		if _, ok := fileToObject[f.Name()]; !ok {
			return nil, fmt.Errorf("key %q does not exist", f.Name())
		}
		if err := yaml.Unmarshal(data, fileToObject[f.Name()]); err != nil {
			return nil, fmt.Errorf("failed to unmarshal file %q: %w", "bla", err)
		}
	}

	// by default, set the namespace to default namespace.
	// the default namespace can be changed via Optional Parameter Function
	f := WithNamespace("")
	f(&mf)

	for _, opt := range opts {
		opt(&mf)
	}

	updateServiceAccountInfo(&mf)

	if err := mf.SetSharedCPUs(sharedCPUs); err != nil {
		return nil, err
	}
	return &mf, nil
}

// WithNewNamespace creates new namespace and updates the objects
func WithNewNamespace(ns string) func(mf *Manifests) {
	return func(mf *Manifests) {
		mf.NS = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		f := WithNamespace(ns)
		f(mf)
	}
}

func WithNamespace(ns string) func(mf *Manifests) {
	return func(mf *Manifests) {
		mf.DS.Namespace = ns
		mf.Role.Namespace = ns
		mf.RB.Namespace = ns
		mf.SA.Namespace = ns
	}
}

func WithName(name string) func(mf *Manifests) {
	return func(mf *Manifests) {
		mf.DS.Name = name
		mf.Role.Name = name
		mf.SA.Name = name
		mf.RB.Name = name
		mf.RB.RoleRef.Name = mf.Role.Name
		mf.SCC.Name = name
	}
}

func (mf *Manifests) ToObjects() []client.Object {
	objs := make([]client.Object, 0)
	if mf.NS.Name != "" {
		objs = append(objs, &mf.NS)
	}
	return append(objs,
		&mf.DS,
		&mf.Role,
		&mf.RB,
		&mf.SA,
		&mf.SCC,
	)
}

// SetSharedCPUs updates the container args under the
// DaemonSet with a --mutual-cpus value.
// It returns an error if the cpus are not a valid cpu set.
func (mf *Manifests) SetSharedCPUs(cpus string) error {
	set, err := cpuset.Parse(cpus)
	if err != nil {
		return fmt.Errorf("failed to set shared cpus; %w", err)
	}
	cnt := &mf.DS.Spec.Template.Spec.Containers[0]
	var newArgs []string
	for _, arg := range cnt.Args {
		keyAndValue := strings.Split(arg, "=")
		if keyAndValue[0] == "--mutual-cpus" {
			continue
		}
		newArgs = append(newArgs, arg)
	}
	newArgs = append(newArgs, fmt.Sprintf("--mutual-cpus=%s", set.String()))
	cnt.Args = newArgs
	return nil
}

func updateServiceAccountInfo(mf *Manifests) {
	saName := mf.SA.Name
	saNS := mf.SA.Namespace

	mf.DS.Spec.Template.Spec.ServiceAccountName = saName
	mf.RB.Subjects[0].Namespace = saNS
	mf.RB.Subjects[0].Name = saName

	sa := saName
	if saNS != "" {
		sa = saNS + ":" + saName
	}
	mf.SCC.Users = []string{
		fmt.Sprintf("system:serviceaccount:%s", sa),
	}
}
