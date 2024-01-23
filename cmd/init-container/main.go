package main

import (
	"context"

	v1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	yaml "sigs.k8s.io/yaml"
)

const webhookManifest string = `
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  annotations:
    capability.openshift.io/name: NodeTuning
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
    include.release.openshift.io/ibm-cloud-managed: "true"
    service.beta.openshift.io/inject-cabundle: "true"
  name: performance-addon-operator
webhooks:
  - admissionReviewVersions:
      - v1
    clientConfig:
      service:
        name: performance-addon-operator-service
        namespace: openshift-cluster-node-tuning-operator
        path: /validate-performance-openshift-io-v2-performanceprofile
        port: 443
    failurePolicy: Fail
    matchPolicy: Equivalent
    name: vwb.performance.openshift.io
    rules:
      - apiGroups:
          - performance.openshift.io
        apiVersions:
          - v2
        operations:
          - CREATE
          - UPDATE
        resources:
          - performanceprofiles
        scope: '*'
    sideEffects: None
    timeoutSeconds: 10
`

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal(err.Error())
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err.Error())
	}
	webhook := &v1.ValidatingWebhookConfiguration{}
	if err := yaml.Unmarshal([]byte(webhookManifest), webhook); err != nil {
		klog.Fatal(err.Error())
	}
	_, err = clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
		context.TODO(), webhook.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.Infof("validating webhook %s not found, creating...", webhook.Name)
			_, err = clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(
				context.TODO(), webhook, metav1.CreateOptions{})
			if err != nil {
				klog.Fatalf("failed to create validating webhook %s: %v", webhook.Name, err)
			}
		}
	}
}
