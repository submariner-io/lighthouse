package framework

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/tools/remotecommand"

	"github.com/submariner-io/admiral/pkg/federate/kubefed"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/federate"

	"github.com/onsi/ginkgo"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	restclient "k8s.io/client-go/rest"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclientset "k8s.io/client-go/kubernetes"

	. "github.com/onsi/gomega"
)

const (
	// Polling interval while trying to create objects
	PollInterval = 100 * time.Millisecond
)

type ClusterIndex int

const (
	ClusterA ClusterIndex = iota
	ClusterB
	ClusterC
)

const (
	SubmarinerEngine = "submariner-engine"
	GatewayLabel     = "submariner.io/gateway"
)

type PatchFunc func(pt types.PatchType, payload []byte) error

type PatchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

type DoOperationFunc func() (interface{}, error)
type CheckResultFunc func(result interface{}) (bool, error)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
// Eventual goal is to merge this with integration test framework.
type Framework struct {
	BaseName string

	// Set together with creating the ClientSet and the namespace.
	// Guaranteed to be unique in the cluster even when running the same
	// test multiple times in parallel.
	UniqueName string

	ClusterClients []*kubeclientset.Clientset
	federator      []federate.Federator

	SkipNamespaceCreation bool            // Whether to skip creating a namespace
	Namespace             string          // Every test has a namespace at least unless creation is skipped
	namespacesToDelete    []*v1.Namespace // Some tests have more than one.

	// To make sure that this framework cleans up after itself, no matter what,
	// we install a Cleanup action before each test and clear it after.  If we
	// should abort, the AfterSuite hook should run all Cleanup actions.
	cleanupHandle CleanupActionHandle

	// configuration for framework's client
	Options Options
}

// Options is a struct for managing test framework options.
type Options struct {
	ClientQPS    float32
	ClientBurst  int
	GroupVersion *schema.GroupVersion
}

// NewDefaultFramework makes a new framework and sets up a BeforeEach/AfterEach for
// you (you can write additional before/after each functions).
func NewDefaultFramework(baseName string) *Framework {
	options := Options{
		ClientQPS:   20,
		ClientBurst: 50,
	}
	return NewFramework(baseName, options)
}

// NewFramework creates a test framework.
func NewFramework(baseName string, options Options) *Framework {
	f := &Framework{
		BaseName: baseName,
		Options:  options,
	}

	ginkgo.BeforeEach(f.BeforeEach)
	ginkgo.AfterEach(f.AfterEach)

	return f
}

func (f *Framework) BeforeEach() {
	// workaround for a bug in ginkgo.
	// https://github.com/onsi/ginkgo/issues/222
	f.cleanupHandle = AddCleanupAction(f.AfterEach)

	ginkgo.By("Creating kubernetes clients")
	for idx, context := range TestContext.KubeContexts {
		client := f.createKubernetesClient(context)
		f.ClusterClients = append(f.ClusterClients, client)
		stopCh := make(chan struct{})
		var federator federate.Federator
		switch ClusterIndex(idx) {
		case ClusterA:
			federator = f.buildKubeFedFederator(stopCh, context)
		}
		f.federator = append(f.federator, federator)
	}

	if !f.SkipNamespaceCreation {
		ginkgo.By(fmt.Sprintf("Building namespace api objects, basename %s", f.BaseName))

		namespaceLabels := map[string]string{
			"e2e-framework": f.BaseName,
		}

		for idx, clientSet := range f.ClusterClients {
			switch ClusterIndex(idx) {
			case ClusterA: // On the first cluster we let k8s generate a name for the namespace
				namespace := generateNamespace(clientSet, f.BaseName, namespaceLabels)
				f.Namespace = namespace.GetName()
				f.UniqueName = namespace.GetName()
				f.AddNamespacesToDelete(namespace)
				namespace.Namespace = namespace.GetObjectMeta().GetName()
				err := f.federator[idx].Distribute(namespace)
				if err != nil {
					Errorf("Unable to load federate  %s for context %s, this is a non-recoverable error",
						TestContext.KubeConfig)
					os.Exit(1)
				}
			default: // On the other clusters we use the same name to make tracing easier
				continue
				//f.CreateNamespace(clientSet, f.Namespace, namespaceLabels)
			}
		}
	} else {
		f.UniqueName = string(uuid.NewUUID())
	}

}

func (f *Framework) createKubernetesClient(context string) *kubeclientset.Clientset {

	restConfig, _, err := loadConfig(TestContext.KubeConfig, context)
	if err != nil {
		Errorf("Unable to load kubeconfig file %s for context %s, this is a non-recoverable error",
			TestContext.KubeConfig, context)
		Errorf("loadConfig err: %s", err.Error())
		os.Exit(1)
	}

	testDesc := ginkgo.CurrentGinkgoTestDescription()
	if len(testDesc.ComponentTexts) > 0 {
		componentTexts := strings.Join(testDesc.ComponentTexts, " ")
		restConfig.UserAgent = fmt.Sprintf(
			"%v -- %v",
			rest.DefaultKubernetesUserAgent(),
			componentTexts)
	}

	restConfig.QPS = f.Options.ClientQPS
	restConfig.Burst = f.Options.ClientBurst
	if f.Options.GroupVersion != nil {
		restConfig.GroupVersion = f.Options.GroupVersion
	}
	clientSet, err := kubeclientset.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred())

	// create scales getter, set GroupVersion and NegotiatedSerializer to default values
	// as they are required when creating a REST client.
	if restConfig.GroupVersion == nil {
		restConfig.GroupVersion = &schema.GroupVersion{}
	}
	if restConfig.NegotiatedSerializer == nil {
		restConfig.NegotiatedSerializer = scheme.Codecs
	}
	return clientSet
}

func (f *Framework) buildKubeFedFederator(stopCh <-chan struct{}, context string) federate.Federator {
	kubeConfig, _, err := loadConfig(TestContext.KubeConfig, context)
	if err != nil {
		klog.Fatalf("Error attempting to load kubeconfig: %s", err.Error())
	}
	federator, err := kubefed.New(kubeConfig, stopCh)
	if err != nil {
		klog.Fatalf("Error creating kubefed federator: %s", err.Error())
	}
	return federator
}

func deleteNamespace(client kubeclientset.Interface, namespaceName string) error {

	return client.CoreV1().Namespaces().Delete(
		namespaceName,
		&metav1.DeleteOptions{})

}

// AfterEach deletes the namespace, after reading its events.
func (f *Framework) AfterEach() {
	RemoveCleanupAction(f.cleanupHandle)

	// DeleteNamespace at the very end in defer, to avoid any
	// expectation failures preventing deleting the namespace.
	defer func() {
		nsDeletionErrors := map[string][]error{}
		// Whether to delete namespace is determined by 3 factors: delete-namespace flag, delete-namespace-on-failure flag and the test result
		// if delete-namespace set to false, namespace will always be preserved.
		// if delete-namespace is true and delete-namespace-on-failure is false, namespace will be preserved if test failed.
		for _, ns := range f.namespacesToDelete {
			ginkgo.By(fmt.Sprintf("Destroying namespace %q for this suite on all clusters.", ns.Name))
			if errors := f.deleteNamespaceFromAllClusters(ns); errors != nil {
				nsDeletionErrors[ns.Name] = errors
			}
		}

		// Paranoia-- prevent reuse!
		f.Namespace = ""
		f.ClusterClients = nil
		f.namespacesToDelete = nil

		// if we had errors deleting, report them now.
		if len(nsDeletionErrors) != 0 {
			messages := []string{}
			for namespaceKey, namespaceErrors := range nsDeletionErrors {
				for clusterIdx, namespaceErr := range namespaceErrors {
					messages = append(messages, fmt.Sprintf("Couldn't delete ns: %q (@cluster %d): %s (%#v)",
						namespaceKey, clusterIdx, namespaceErr, namespaceErr))
				}
			}
			Failf(strings.Join(messages, ","))
		}
	}()

}

func (f *Framework) deleteNamespaceFromAllClusters(ns *v1.Namespace) []error {
	var errors []error
	for _, clientSet := range f.ClusterClients {
		if err := deleteNamespace(clientSet, ns.Name); err != nil {
			switch {
			case apierrors.IsNotFound(err):
				Logf("Namespace %v was already deleted", ns.Name)
			case apierrors.IsConflict(err):
				Logf("Namespace %v scheduled for deletion, resources being purged", ns.Name)
			default:
				Logf("Failed deleting namespace: %v", err)
				errors = append(errors, err)
			}
		}
	}
	return errors
}

func (f *Framework) AddNamespacesToDelete(namespaces ...*v1.Namespace) {
	for _, ns := range namespaces {
		if ns == nil {
			continue
		}
		f.namespacesToDelete = append(f.namespacesToDelete, ns)
	}
}

func generateNamespace(client kubeclientset.Interface, baseName string, labels map[string]string) *v1.Namespace {
	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("e2e-tests-%v-", baseName),
			Labels:       labels,
		},
	}

	namespace, err := client.CoreV1().Namespaces().Create(namespaceObj)
	Expect(err).NotTo(HaveOccurred(), "Error generating namespace %v", namespaceObj)
	return namespace
}

// AwaitUntil periodically performs the given operation until the given CheckResultFunc returns true, an error, or a
// timeout is reached.
func AwaitUntil(opMsg string, doOperation DoOperationFunc, checkResult CheckResultFunc) interface{} {
	var finalResult interface{}
	err := wait.PollImmediate(5*time.Second, 1*time.Minute, func() (bool, error) {
		result, err := doOperation()
		if err != nil {
			if IsTransientError(err, opMsg) {
				return false, nil
			}
			return false, err
		}

		ok, err := checkResult(result)
		if err != nil {
			return false, err
		}

		if ok {
			finalResult = result
			return true, nil
		}

		return false, nil
	})

	Expect(err).NotTo(HaveOccurred(), "Failed to "+opMsg)
	return finalResult
}

func NoopCheckResult(interface{}) (bool, error) {
	return true, nil
}

// ExecOptions passed to ExecWithOptions
type ExecOptions struct {
	Command []string

	Namespace     string
	PodName       string
	ContainerName string

	Stdin         io.Reader
	CaptureStdout bool
	CaptureStderr bool
	// If false, whitespace in std{err,out} will be removed.
	PreserveWhitespace bool
}

// ExecWithOptions executes a command in the specified container,
// returning stdout, stderr and error. `options` allowed for
// additional parameters to be passed.
// ExecWithOptions executes a command in the specified container,
// returning stdout, stderr and error. `options` allowed for
// additional parameters to be passed.
func (f *Framework) ExecWithOptions(options ExecOptions, index ClusterIndex) (string, string, error) {
	Logf("ExecWithOptions %+v", options)
	config, _, err := loadConfig(TestContext.KubeConfig, TestContext.KubeContexts[index])
	const tty = false
	if err != nil {
		Logf("ExecWithOptions %+v failed %+v", options, err)
		os.Exit(1)
	}

	req := f.ClusterClients[index].CoreV1().RESTClient().Post().
		Resource("pods").
		Name(options.PodName).
		Namespace(options.Namespace).
		SubResource("exec").
		Param("container", options.ContainerName)
	req.VersionedParams(&v1.PodExecOptions{
		Container: options.ContainerName,
		Command:   options.Command,
		Stdin:     options.Stdin != nil,
		Stdout:    options.CaptureStdout,
		Stderr:    options.CaptureStderr,
		TTY:       tty,
	}, scheme.ParameterCodec)

	var stdout, stderr bytes.Buffer
	attempts := 5
	for ; attempts > 0; attempts-- {
		err = execute("POST", req.URL(), config, options.Stdin, &stdout, &stderr, tty)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 5000)
		Logf("Retrying due to error  %+v", err)
	}
	if options.PreserveWhitespace {
		return stdout.String(), stderr.String(), err
	}
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func execute(method string, url *url.URL, config *restclient.Config, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	exec, err := remotecommand.NewSPDYExecutor(config, method, url)
	if err != nil {
		return err
	}
	return exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    tty,
	})
}
