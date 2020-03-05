package framework

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/federate/kubefed"
	"github.com/submariner-io/submariner/test/e2e/framework"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog"
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace.
type Framework struct {
	*framework.Framework
}

// New makes a new framework and sets up a BeforeEach/AfterEach
func New(baseName string) *Framework {
	f := &Framework{Framework: framework.NewDefaultFramework(baseName)}
	ginkgo.BeforeEach(f.beforeEach)
	return f
}

func (f *Framework) beforeEach() {
	if !f.SkipNamespaceCreation {
		ginkgo.By(fmt.Sprintf("Federating Namespace %q", f.Namespace))

		namespace, err := f.ClusterClients[framework.ClusterA].CoreV1().Namespaces().Get(f.Namespace, metav1.GetOptions{})
		Expect(err).To(Succeed(), "Error retrieving namespace %q", f.Namespace)
		namespace.Namespace = namespace.GetObjectMeta().GetName()

		federator := f.buildKubeFedFederator(framework.TestContext.KubeContexts[framework.ClusterA])
		err = federator.Distribute(namespace)
		Expect(err).To(Succeed(), "Error distributing namespace %v", namespace)
	}
}

func (f *Framework) buildKubeFedFederator(context string) federate.Federator {
	kubeConfig, _, err := loadConfig(framework.TestContext.KubeConfig, context)
	if err != nil {
		klog.Fatalf("Error attempting to load kubeconfig: %s", err.Error())
	}

	federator, err := kubefed.New(kubeConfig, make(chan struct{}))
	if err != nil {
		klog.Fatalf("Error creating kubefed federator: %s", err.Error())
	}
	return federator
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
func (f *Framework) ExecWithOptions(options ExecOptions, index framework.ClusterIndex) (string, string, error) {
	framework.Logf("ExecWithOptions %+v", options)

	config, _, err := loadConfig(framework.TestContext.KubeConfig, framework.TestContext.KubeContexts[index])
	Expect(err).To(Succeed(), fmt.Sprintf("ExecWithOptions %#v", options))

	const tty = false
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
		framework.Logf("Retrying due to error  %+v", err)
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
