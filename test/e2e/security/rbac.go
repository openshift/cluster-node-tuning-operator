// Assisted-by: OpenCode; model: Qwen 3.8 27B

package e2e

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// ClusterRole bound to the operator ServiceAccount must not grant
// cluster-wide ConfigMap/DaemonSet/OLM mutation beyond the operator's
// operational need.
//
// The checks are two-fold:
//  1. Static inspection of the ClusterRole/Role rules, the only way to
//     verify that namespace-scoped permissions live in namespaced Roles.
//  2. A live proof-of-concept for the "operator-pod or token compromise"
//     attack: we request the operator ServiceAccount's real token via the
//     TokenRequest API (as cluster admin, i.e. standing in for an attacker
//     who stole the token from the compromised pod) and then attempt the
//     actual attacks with it: creating a DaemonSet in another namespace,
//     reading/writing ConfigMaps in its own and other namespaces, deleting
//     other operators' Subscriptions/CSVs, and using an SCC other than
//     "restricted-v3" (via SubjectAccessReview: the SCC admission plugin
//     gates pod creation with an RBAC "use" check on the named SCC, which
//     a real request cannot express but a SubjectAccessReview can).
var _ = ginkgo.Describe("[security][rbac] Node Tuning Operator least-privilege RBAC", func() {
	const (
		operatorClusterRoleName = "cluster-node-tuning-operator"
		operatorRoleName        = "cluster-node-tuning-operator"
		extensionAuthConfigMap  = "extension-apiserver-authentication"
		olmGroup                = "operators.coreos.com"
		perfGroup               = "performance.openshift.io"
		pocName                 = "nto-rbac-poc"
	)

	var (
		ctx        context.Context
		cancel     context.CancelFunc
		adminKcs   kubernetes.Interface
		operatorNS = ntoconfig.WatchNamespace()
		operatorSA = fmt.Sprintf("system:serviceaccount:%s:%s", operatorNS, operatorClusterRoleName)
	)

	ginkgo.BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
		kubeconfig, err := ntoclient.GetConfig()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		adminKcs = kubernetes.NewForConfigOrDie(kubeconfig)
	})

	ginkgo.AfterEach(func() {
		cancel()
	})

	ginkgo.It("must not grant the operator ClusterRole privileges beyond its operational need", func() {
		clusterRole, err := adminKcs.RbacV1().ClusterRoles().Get(ctx, operatorClusterRoleName, metav1.GetOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "operator ClusterRole %s not found", operatorClusterRoleName)

		rulesFor := func(group, resource string) []rbacv1.PolicyRule {
			var matched []rbacv1.PolicyRule
			for _, r := range clusterRole.Rules {
				if slices.Contains(r.APIGroups, group) && slices.Contains(r.Resources, resource) {
					matched = append(matched, r)
				}
			}
			return matched
		}

		ginkgo.By("checking that no rule uses wildcards")
		for _, r := range clusterRole.Rules {
			gomega.Expect(r.APIGroups).NotTo(gomega.ContainElement("*"),
				"ClusterRole rule uses an API group wildcard: %+v", r)
			gomega.Expect(r.Resources).NotTo(gomega.ContainElement("*"),
				"ClusterRole rule uses a resource wildcard: %+v", r)
			gomega.Expect(r.Verbs).NotTo(gomega.ContainElement("*"),
				"ClusterRole rule uses a verb wildcard: %+v", r)
		}

		ginkgo.By("checking that no OLM (operators.coreos.com) rule exists")
		for _, r := range clusterRole.Rules {
			gomega.Expect(r.APIGroups).NotTo(gomega.ContainElement(olmGroup),
				"ClusterRole grants access to OLM resources: %+v", r)
		}

		ginkgo.By("checking that the ClusterRole does not grant any DaemonSet access (it is namespaced)")
		gomega.Expect(rulesFor("apps", "daemonsets")).To(gomega.BeEmpty(),
			"ClusterRole grants cluster-wide DaemonSet access; it should be namespaced")

		ginkgo.By("checking that SCC use is limited to \"restricted-v3\"")
		for _, r := range rulesFor("security.openshift.io", "securitycontextconstraints") {
			gomega.Expect(r.ResourceNames).To(gomega.Equal([]string{"restricted-v3"}),
				"SCC rule is not pinned to resourceNames [restricted-v3]: %+v", r)
		}

		ginkgo.By("checking that the ClusterRole does not grant any ConfigMap access (it is namespaced)")
		gomega.Expect(rulesFor("", "configmaps")).To(gomega.BeEmpty(),
			"ClusterRole grants cluster-wide ConfigMap access; it should be namespaced")

		ginkgo.By("checking that the ClusterRole does not grant any Events access")
		gomega.Expect(rulesFor("", "events")).To(gomega.BeEmpty(),
			"ClusterRole grants cluster-wide Events access; it should be namespaced")

		ginkgo.By("checking that the ClusterRole does not grant any Lease access (it is namespaced)")
		gomega.Expect(rulesFor("coordination.k8s.io", "leases")).To(gomega.BeEmpty(),
			"ClusterRole grants cluster-wide Lease access; it should be namespaced")

		ginkgo.By("checking that performance.openshift.io access is limited to performanceprofiles")
		allowedPerfResources := []string{
			"performanceprofiles", "performanceprofiles/status", "performanceprofiles/finalizers",
		}
		for _, r := range clusterRole.Rules {
			if !slices.Contains(r.APIGroups, perfGroup) {
				continue
			}
			for _, res := range r.Resources {
				gomega.Expect(slices.Contains(allowedPerfResources, res)).To(gomega.BeTrue(),
					"performance.openshift.io rule grants unexpected resource %q: %+v", res, r)
			}
		}

		ginkgo.By("checking that the operator Role grants DaemonSet, Lease and Events access in its own namespace")
		role, err := adminKcs.RbacV1().Roles(operatorNS).Get(ctx, operatorRoleName, metav1.GetOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "operator Role %s/%s not found", operatorNS, operatorRoleName)
		foundLeaseRule := false
		for _, r := range role.Rules {
			// No ConfigMap rule: nothing running with this identity reads or
			// writes ConfigMaps in the operator namespace.  The MachineConfig
			// ConfigMaps are managed through the management cluster identity
			// (HyperShift) and the metrics CA bundle lives in kube-system.
			gomega.Expect(r.Resources).NotTo(gomega.ContainElement("configmaps"),
				"operator Role grants ConfigMap access in its own namespace; no code path uses it: %+v", r)
			if slices.Contains(r.Resources, "events") {
				gomega.Expect(r.Verbs).To(gomega.ContainElement("create"))
			}
			if slices.Contains(r.Resources, "daemonsets") {
				gomega.Expect(r.Verbs).To(gomega.ContainElement("create"))
				gomega.Expect(r.Verbs).To(gomega.ContainElement("list"))
				gomega.Expect(r.Verbs).To(gomega.ContainElement("watch"))
			}
			// The client-go lease lock (used by the controller-runtime
			// manager for leader election) performs only create/get/update.
			// Leader election is always on, so the rule must exist.
			if slices.Contains(r.Resources, "leases") {
				foundLeaseRule = true
				gomega.Expect(r.Verbs).To(gomega.ContainElement("create"))
				gomega.Expect(r.Verbs).To(gomega.ContainElement("get"))
				gomega.Expect(r.Verbs).To(gomega.ContainElement("update"))
			}
		}
		gomega.Expect(foundLeaseRule).To(gomega.BeTrue(),
			"operator Role has no Lease rule; leader election would break")

		ginkgo.By("checking that the kube-system Role grants only read access to ConfigMaps")
		kubeSysRole, err := adminKcs.RbacV1().Roles("kube-system").Get(ctx, operatorRoleName, metav1.GetOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Role %s/kube-system not found", operatorRoleName)
		foundCMRule := false
		for _, r := range kubeSysRole.Rules {
			if !slices.Contains(r.Resources, "configmaps") {
				continue
			}
			foundCMRule = true
			gomega.Expect(r.ResourceNames).To(gomega.Equal([]string{extensionAuthConfigMap}),
				"kube-system Role ConfigMap rule is not pinned to resourceNames [%s]: %+v", extensionAuthConfigMap, r)
			for _, v := range r.Verbs {
				gomega.Expect(slices.Contains([]string{"get", "list", "watch"}, v)).To(gomega.BeTrue(),
					"kube-system Role grants a non-read verb %q: %+v", v, r)
			}
		}
		gomega.Expect(foundCMRule).To(gomega.BeTrue(), "kube-system Role has no ConfigMap rule")
		kubeSysRB, err := adminKcs.RbacV1().RoleBindings("kube-system").Get(ctx, operatorRoleName, metav1.GetOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "RoleBinding %s/kube-system not found", operatorRoleName)
		subjectFound := false
		for _, s := range kubeSysRB.Subjects {
			if s.Kind == "ServiceAccount" && s.Name == operatorClusterRoleName && s.Namespace == operatorNS {
				subjectFound = true
			}
		}
		gomega.Expect(subjectFound).To(gomega.BeTrue(),
			"kube-system RoleBinding does not reference the operator ServiceAccount")
	})

	ginkgo.It("PoC: a compromised operator token cannot escape its namespace and named resources", func() {
		ginkgo.By("stealing the operator ServiceAccount token (TokenRequest as cluster admin)")
		tokenExpiration := int64(600)
		tokenResponse, err := adminKcs.CoreV1().ServiceAccounts(operatorNS).
			CreateToken(ctx, operatorClusterRoleName, &authenticationv1.TokenRequest{
				Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &tokenExpiration},
			}, metav1.CreateOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(),
			"failed to request a token for %s", operatorSA)
		gomega.Expect(tokenResponse.Status.Token).NotTo(gomega.BeEmpty())

		// Build a client config that authenticates exclusively with the
		// stolen token.  Only the server-CA trust is copied from the test
		// kubeconfig; client certificates, tokenFiles, exec plugins and basic
		// auth are deliberately dropped, because any of them would take
		// precedence over the BearerToken (a client certificate is accepted
		// by the API server ahead of the token) and make the "attacker"
		// requests run as the test user instead of the operator SA.
		baseConfig, err := ntoclient.GetConfig()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		tokenOnlyConfig := func(token string) *rest.Config {
			tls := baseConfig.TLSClientConfig
			return &rest.Config{
				Host:        baseConfig.Host,
				BearerToken: token,
				TLSClientConfig: rest.TLSClientConfig{
					Insecure:   tls.Insecure,
					ServerName: tls.ServerName,
					CAFile:     tls.CAFile,
					CAData:     tls.CAData,
				},
			}
		}
		attacker := kubernetes.NewForConfigOrDie(tokenOnlyConfig(tokenResponse.Status.Token))

		ginkgo.By("verifying the PoC client authenticates as the operator ServiceAccount")
		// A SelfSubjectReview returns the identity the API server sees for
		// this client; if it is not the operator SA, the test kubeconfig
		// credentials leaked in and the attack results would be invalid.
		review, err := attacker.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(review.Status.UserInfo.Username).To(gomega.Equal(operatorSA),
			"PoC client authenticates as %q instead of %q; the attack results would be invalid",
			review.Status.UserInfo.Username, operatorSA)

		ginkgo.By("sanity check: the stolen token authenticates and works inside the operator namespace")
		_, err = attacker.CoreV1().Events(operatorNS).List(ctx, metav1.ListOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(),
			"the stolen token must work inside the operator namespace, got: %v", err)

		ginkgo.By("sanity check: the metrics CA bundle read (required for certificate rotation) still works")
		_, err = attacker.CoreV1().ConfigMaps("kube-system").Get(ctx, extensionAuthConfigMap, metav1.GetOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(),
			"the operator must be able to read the metrics CA bundle ConfigMap, got: %v", err)

		// expectDenied asserts the attack was rejected by authorization.
		// A nil error or NotFound means the token was actually authorized:
		// the vulnerability is present.
		expectDenied := func(desc string, err error) {
			if err == nil {
				ginkgo.Fail(fmt.Sprintf("PoC succeeded: the operator token could %s (RBAC fix missing)", desc))
			}
			gomega.Expect(k8serrors.IsForbidden(err)).To(gomega.BeTrue(),
				"expected Forbidden for %q, got: %v", desc, err)
		}

		// inertPodSpec returns a pod spec that can never run: it is pinned
		// to a nonexistent node and uses a nonexistent image.
		inertPodSpec := func() corev1.PodSpec {
			automount := false
			return corev1.PodSpec{
				ServiceAccountName:           operatorClusterRoleName,
				NodeSelector:                 map[string]string{"kubernetes.io/hostname": pocName + "-nonexistent-node"},
				AutomountServiceAccountToken: &automount,
				Containers: []corev1.Container{{
					Name:    pocName,
					Image:   "registry.invalid/" + pocName + ":nonexistent",
					Command: []string{"sleep", "100000"},
				}},
			}
		}

		ginkgo.By("PoC: creating a DaemonSet in kube-system")
		pocDaemonSet := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: pocName, Namespace: "kube-system",
				Labels: map[string]string{"app": pocName}},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": pocName}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": pocName}},
					Spec:       inertPodSpec(),
				},
			},
		}
		created, err := attacker.AppsV1().DaemonSets("kube-system").Create(ctx, pocDaemonSet, metav1.CreateOptions{})
		if err == nil {
			util.Logf("PoC: created inert DaemonSet %s/%s, cleaning up", created.Namespace, created.Name)
			gomega.Expect(adminKcs.AppsV1().DaemonSets(created.Namespace).Delete(ctx, created.Name, metav1.DeleteOptions{})).
				NotTo(gomega.HaveOccurred())
		}
		expectDenied("create a DaemonSet in kube-system", err)

		ginkgo.By("PoC: listing ConfigMaps in openshift-monitoring")
		// Reading kube-system ConfigMaps is legitimate (metrics CA bundle),
		// so target a genuinely foreign namespace.  List is used because it
		// does not depend on a specific ConfigMap name existing.
		cms, err := attacker.CoreV1().ConfigMaps("openshift-monitoring").List(ctx, metav1.ListOptions{})
		if err == nil {
			util.Logf("PoC: listed %d ConfigMaps in openshift-monitoring with the operator token", len(cms.Items))
		}
		expectDenied("list ConfigMaps in openshift-monitoring", err)

		ginkgo.By("PoC: creating a ConfigMap in openshift-monitoring")
		pocConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: pocName, Namespace: "openshift-monitoring"},
			Data:       map[string]string{pocName: "true"},
		}
		createdCM, err := attacker.CoreV1().ConfigMaps("openshift-monitoring").Create(ctx, pocConfigMap, metav1.CreateOptions{})
		if err == nil {
			util.Logf("PoC: created ConfigMap %s/%s, cleaning up", createdCM.Namespace, createdCM.Name)
			gomega.Expect(adminKcs.CoreV1().ConfigMaps(createdCM.Namespace).Delete(ctx, createdCM.Name, metav1.DeleteOptions{})).
				NotTo(gomega.HaveOccurred())
		}
		expectDenied("create a ConfigMap in openshift-monitoring", err)

		ginkgo.By("PoC: creating a ConfigMap in the operator namespace")
		// The tightened Role grants no ConfigMap access at all in the
		// operator namespace: the MachineConfig ConfigMaps are managed
		// through the management cluster identity (HyperShift) and the
		// metrics CA bundle lives in kube-system.
		pocOwnNSConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: pocName, Namespace: operatorNS},
			Data:       map[string]string{pocName: "true"},
		}
		createdOwnCM, err := attacker.CoreV1().ConfigMaps(operatorNS).Create(ctx, pocOwnNSConfigMap, metav1.CreateOptions{})
		if err == nil {
			util.Logf("PoC: created ConfigMap %s/%s, cleaning up", createdOwnCM.Namespace, createdOwnCM.Name)
			gomega.Expect(adminKcs.CoreV1().ConfigMaps(createdOwnCM.Namespace).Delete(ctx, createdOwnCM.Name, metav1.DeleteOptions{})).
				NotTo(gomega.HaveOccurred())
		}
		expectDenied("create a ConfigMap in "+operatorNS, err)

		ginkgo.By("PoC: verifying SCC use is pinned to restricted-v3 (SubjectAccessReview)")
		// The SCC admission plugin authorizes pod creation with an RBAC
		// "use" check on the specific SCC: the evaluator matches the
		// requested SCC name against the rule's resourceNames.  A real
		// request cannot name the SCC it wants to use, but a
		// SubjectAccessReview can (ResourceAttributes.Name), so these
		// checks verify the resourceNames pinning live: restricted-v3
		// (the operator pod's SCC) must be usable, anyuid must not.
		checkSCCUse := func(scc string, wantAllowed bool) {
			review, err := adminKcs.AuthorizationV1().SubjectAccessReviews().Create(ctx,
				&authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: operatorSA,
						Groups: []string{
							"system:authenticated",
							"system:serviceaccounts",
							"system:serviceaccounts:" + operatorNS,
						},
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Group:    "security.openshift.io",
							Resource: "securitycontextconstraints",
							Verb:     "use",
							Name:     scc,
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(review.Status.Allowed).To(gomega.Equal(wantAllowed),
				"SCC %q: expected use allowed=%v for %s, got allowed=%v", scc, wantAllowed, operatorSA, review.Status.Allowed)
		}
		checkSCCUse("restricted-v3", true)
		checkSCCUse("anyuid", false)

		ginkgo.By("PoC: deleting other operators' Subscriptions and CSVs")
		// Deleting nonexistent objects keeps the PoC non-destructive: before
		// the fix the request is authorized and returns NotFound, after the
		// fix it is denied with Forbidden.
		groups, err := adminKcs.Discovery().ServerGroups()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		olmGVRs := map[string]schema.GroupVersionResource{}
		for _, g := range groups.Groups {
			if g.Name != olmGroup {
				continue
			}
			for _, gv := range g.Versions {
				resources, err := adminKcs.Discovery().ServerResourcesForGroupVersion(gv.GroupVersion)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					"discovery of resources for group version %s failed", gv.GroupVersion)
				for _, r := range resources.APIResources {
					if r.Name == "subscriptions" || r.Name == "clusterserviceversions" {
						olmGVRs[r.Name] = schema.GroupVersionResource{Group: olmGroup, Version: gv.Version, Resource: r.Name}
					}
				}
			}
		}
		subGVR, subOK := olmGVRs["subscriptions"]
		csvGVR, csvOK := olmGVRs["clusterserviceversions"]
		if !subOK || !csvOK {
			ginkgo.Skip("OLM (operators.coreos.com) API not present, skipping OLM PoC checks")
		}

		dyn := dynamic.NewForConfigOrDie(tokenOnlyConfig(tokenResponse.Status.Token))
		err = dyn.Resource(subGVR).Namespace(operatorNS).Delete(ctx, pocName+"-nonexistent", metav1.DeleteOptions{})
		expectDenied("delete a Subscription in "+operatorNS, err)
		err = dyn.Resource(csvGVR).Namespace(operatorNS).Delete(ctx, pocName+"-nonexistent", metav1.DeleteOptions{})
		expectDenied("delete a ClusterServiceVersion", err)
	})
})
