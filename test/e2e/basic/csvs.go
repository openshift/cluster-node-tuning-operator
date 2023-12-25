package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/yaml"

	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	paocontroller "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller"
)

const (
	namespaceName                     = "cluster-service-version-test-ns"
	performanceOperatorDeploymentName = "performance-operator"
)

var _ = Describe("[basic][clusterserviceversion] ClusterServiceVersion listing", Ordered, func() {
	var cli client.Client
	var paginationLimit uint64
	var namespace corev1.Namespace
	var baseOptions []client.ListOption
	BeforeAll(func() {
		var err error

		By("Getting new client")
		cli, err = newClient()
		Expect(err).To(Succeed())

		By("Creating new test namespace ...")
		randomize := true
		namespace, err = setupNamespace(cli, namespaceName, randomize)
		Expect(err).To(Succeed())

		By(fmt.Sprintf("New test namespace %s created", namespace.Name))

		baseOptions = append(baseOptions, client.InNamespace(namespace.Name))
	})
	AfterAll(func() {
		var err error

		baseOptions = nil

		By(fmt.Sprintf("Delete test namespace (%s) ", namespace.Name))
		err = cli.Delete(context.TODO(), &namespace)
		Expect(err).To(Succeed())
	})

	When("there is no CSVs in the cluster", func() {
		It("Should list CSVs without timeout", func() {
			ret, err := paocontroller.ListPerformanceOperatorCSVs(cli, baseOptions, paginationLimit, performanceOperatorDeploymentName)
			Expect(err).To(Succeed())
			Expect(ret).To(HaveLen(0))
		})
	})

	When("there are few CSVs in the cluster", func() {
		const csvsFileRelativePath = "../testing_manifests/csv-dummy.yaml"
		const numberCSVsToCreate = 1000

		BeforeAll(func() {
			var err error

			By("reading file")
			b, err := os.ReadFile(csvsFileRelativePath)
			Expect(err).To(Succeed())

			By("unmarshaling file")
			csvsOrig := olmv1alpha1.ClusterServiceVersion{}
			err = yaml.Unmarshal(b, &csvsOrig)
			Expect(err).To(Succeed())

			By(fmt.Sprintf("Create some(%d) CSVs ... (this gonna take some time)", numberCSVsToCreate))
			csvsCreated := []types.NamespacedName{}
			for idx := 0; idx < numberCSVsToCreate; idx++ {
				csvs := csvsOrig.DeepCopy()
				csvs.Name = fmt.Sprintf("csvs-test-%06d", idx)
				csvs.Namespace = namespace.Name

				err = cli.Create(context.TODO(), csvs)
				Expect(err).To(Succeed())
				key := types.NamespacedName{
					Name:      csvs.Name,
					Namespace: csvs.Namespace,
				}
				csvsCreated = append(csvsCreated, key)
			}
			Expect(len(csvsCreated)).To(Equal(numberCSVsToCreate))

			//NOTE - As all the resources have been created into a test namespace
			// and that namespace is gonna be deleted in the `Describe`'s `AfterAll` node
			// there is no need to explicitly delete created CSVSs
		})

		When("pagination value is set to ONE", func() {
			BeforeAll(func() {
				By("Setting pagination to ONE")
				paginationLimit = 1
			})
			It("Should list CSVs without timeout event with low pagination", func() {
				By(fmt.Sprintf("Listing elements with pagination %d (this gonna take some time)", paginationLimit))
				ret, err := paocontroller.ListPerformanceOperatorCSVs(cli, baseOptions, paginationLimit, performanceOperatorDeploymentName)
				Expect(err).To(Succeed())
				Expect(ret).To(HaveLen(0))
			})
		})

		When("pagination value is set to a much greater value than the CVS number", func() {
			BeforeAll(func() {
				times := 100
				paginationLimit = uint64(times * numberCSVsToCreate)
				By(fmt.Sprintf("Setting paginationLimit to (%d), that is %d times number of csvs created (%d)", paginationLimit, times, numberCSVsToCreate))
			})
			It("Should list CSVs without timeout even with pagination ", func() {
				By(fmt.Sprintf("Listing elements with pagination %d", paginationLimit))
				ret, err := paocontroller.ListPerformanceOperatorCSVs(cli, baseOptions, paginationLimit, performanceOperatorDeploymentName)
				Expect(err).To(Succeed())
				Expect(ret).To(HaveLen(0))
			})
		})
	})
})

func newClient() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	c, err := client.New(cfg, client.Options{})
	return c, err
}

func setupNamespace(cli client.Client, baseName string, randomize bool) (corev1.Namespace, error) {
	validationErrors := validation.IsDNS1123Label(baseName)
	if len(validationErrors) > 0 {
		return corev1.Namespace{}, fmt.Errorf("Invalid value for namespace name (%s). errors: %v ", baseName, validationErrors)
	}
	name := baseName
	if randomize {
		// intentionally avoid GenerateName like the k8s e2e framework does
		name = randomizeName(baseName)
	}
	ns := newNamespace(name)
	err := cli.Create(context.TODO(), ns)
	if err != nil {
		return *ns, err
	}

	// again we do like the k8s e2e framework does and we try to be robust
	var updatedNs corev1.Namespace
	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		err := cli.Get(ctx, client.ObjectKeyFromObject(ns), &updatedNs)
		if err != nil {
			return false, err
		}
		return true, nil
	})
	return updatedNs, err
}
func randomizeName(baseName string) string {
	return fmt.Sprintf("%s-%s", baseName, strconv.Itoa(rand.Intn(10000)))
}

func newNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}
