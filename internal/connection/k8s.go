package connection

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// K8sConnection implements Connection for K8s pods via the Kubernetes API.
//
// File operations and command execution use the client-go remotecommand package
// (SPDY exec) instead of shelling out to kubectl.
type K8sConnection struct {
	// PodName is the K8s pod name (e.g., "gt-gastown-polecat-alpha").
	PodName string

	// Namespace is the K8s namespace (e.g., "gastown-test").
	Namespace string

	// Container is the container name within the pod. Empty means default container.
	Container string

	// KubeConfig is the path to kubeconfig. Empty means default (in-cluster or ~/.kube/config).
	KubeConfig string

	// execTimeout is the timeout for exec commands.
	execTimeout time.Duration

	// restConfig is lazily initialized from KubeConfig.
	restConfig *rest.Config
	configOnce sync.Once
	configErr  error
}

// K8sConnectionConfig holds configuration for creating a K8sConnection.
type K8sConnectionConfig struct {
	PodName    string
	Namespace  string
	Container  string
	KubeConfig string
}

// DefaultExecTimeout is the default timeout for exec commands.
const DefaultExecTimeout = 30 * time.Second

// NewK8sConnection creates a Connection to a K8s pod.
func NewK8sConnection(cfg K8sConnectionConfig) *K8sConnection {
	return &K8sConnection{
		PodName:     cfg.PodName,
		Namespace:   cfg.Namespace,
		Container:   cfg.Container,
		KubeConfig:  cfg.KubeConfig,
		execTimeout: DefaultExecTimeout,
	}
}

// Name returns the pod name as the connection identifier.
func (c *K8sConnection) Name() string {
	return c.PodName
}

// IsLocal returns false -- K8s connections are remote.
func (c *K8sConnection) IsLocal() bool {
	return false
}

// getRESTConfig returns the Kubernetes REST config, initializing it lazily.
func (c *K8sConnection) getRESTConfig() (*rest.Config, error) {
	c.configOnce.Do(func() {
		if c.KubeConfig != "" {
			c.restConfig, c.configErr = clientcmd.BuildConfigFromFlags("", c.KubeConfig)
		} else {
			// Try in-cluster first, then default kubeconfig.
			c.restConfig, c.configErr = rest.InClusterConfig()
			if c.configErr != nil {
				rules := clientcmd.NewDefaultClientConfigLoadingRules()
				c.restConfig, c.configErr = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
					rules, &clientcmd.ConfigOverrides{}).ClientConfig()
			}
		}
		if c.restConfig != nil {
			c.restConfig.ContentConfig.GroupVersion = &corev1.SchemeGroupVersion
			c.restConfig.ContentConfig.NegotiatedSerializer = serializer.NewCodecFactory(
				runtime.NewScheme(),
			)
		}
	})
	return c.restConfig, c.configErr
}

// podExec runs a command inside the pod via the Kubernetes exec API.
func (c *K8sConnection) podExec(stdin []byte, cmd string, args ...string) ([]byte, error) {
	config, err := c.getRESTConfig()
	if err != nil {
		return nil, &ConnectionError{
			Op:      "exec",
			Machine: c.PodName,
			Err:     fmt.Errorf("building kube config: %w", err),
		}
	}

	// Build the exec request URL.
	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, &ConnectionError{
			Op:      "exec",
			Machine: c.PodName,
			Err:     fmt.Errorf("creating REST client: %w", err),
		}
	}

	command := append([]string{cmd}, args...)
	req := restClient.Post().
		Resource("pods").
		Name(c.PodName).
		Namespace(c.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: c.Container,
			Command:   command,
			Stdin:     len(stdin) > 0,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, req.URL())
	if err != nil {
		return nil, &ConnectionError{
			Op:      "exec",
			Machine: c.PodName,
			Err:     fmt.Errorf("creating SPDY executor: %w", err),
		}
	}

	var stdout, stderr bytes.Buffer
	streamOpts := remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}
	if len(stdin) > 0 {
		streamOpts.Stdin = bytes.NewReader(stdin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.execTimeout)
	defer cancel()

	err = executor.StreamWithContext(ctx, streamOpts)
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			return nil, &ConnectionError{
				Op:      "exec",
				Machine: c.PodName,
				Err:     fmt.Errorf("timed out after %v", c.execTimeout),
			}
		}
		if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "No such file") {
			return nil, &NotFoundError{Path: strings.Join(args, " ")}
		}
		if strings.Contains(errMsg, "Permission denied") {
			return nil, &PermissionError{Path: strings.Join(args, " "), Op: cmd}
		}
		return nil, &ConnectionError{
			Op:      "exec",
			Machine: c.PodName,
			Err:     fmt.Errorf("%s: %w", errMsg, err),
		}
	}

	return stdout.Bytes(), nil
}

// ReadFile reads a file from the pod.
func (c *K8sConnection) ReadFile(path string) ([]byte, error) {
	return c.podExec(nil, "cat", path)
}

// WriteFile writes data to a file in the pod.
func (c *K8sConnection) WriteFile(path string, data []byte, perm fs.FileMode) error {
	// Use tee to write stdin to the file
	_, err := c.podExec(data, "tee", path)
	if err != nil {
		return err
	}
	// Set permissions
	_, err = c.podExec(nil, "chmod", fmt.Sprintf("%o", perm), path)
	return err
}

// MkdirAll creates directories in the pod.
func (c *K8sConnection) MkdirAll(path string, perm fs.FileMode) error {
	_, err := c.podExec(nil, "mkdir", "-p", path)
	if err != nil {
		return err
	}
	_, err = c.podExec(nil, "chmod", fmt.Sprintf("%o", perm), path)
	return err
}

// Remove removes a file or empty directory in the pod.
func (c *K8sConnection) Remove(path string) error {
	_, err := c.podExec(nil, "rm", path)
	return err
}

// RemoveAll removes a file or directory tree in the pod.
func (c *K8sConnection) RemoveAll(path string) error {
	_, err := c.podExec(nil, "rm", "-rf", path)
	return err
}

// Stat returns file info for a path in the pod.
func (c *K8sConnection) Stat(path string) (FileInfo, error) {
	// Use stat with a parseable format
	out, err := c.podExec(nil, "stat", "-c", "%n|%s|%a|%Y|%F", path)
	if err != nil {
		return nil, err
	}
	return parseStatOutput(string(out))
}

// parseStatOutput parses the output of stat -c "%n|%s|%a|%Y|%F".
func parseStatOutput(output string) (FileInfo, error) {
	output = strings.TrimSpace(output)
	parts := strings.SplitN(output, "|", 5)
	if len(parts) < 5 {
		return nil, fmt.Errorf("unexpected stat output: %s", output)
	}

	size, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing size: %w", err)
	}

	modeVal, err := strconv.ParseUint(parts[2], 8, 32)
	if err != nil {
		return nil, fmt.Errorf("parsing mode: %w", err)
	}

	epoch, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing mtime: %w", err)
	}

	isDir := strings.Contains(parts[4], "directory")

	return BasicFileInfo{
		FileName:    parts[0],
		FileSize:    size,
		FileMode:    fs.FileMode(modeVal),
		FileModTime: time.Unix(epoch, 0),
		FileIsDir:   isDir,
	}, nil
}

// Glob returns files matching the pattern in the pod.
func (c *K8sConnection) Glob(pattern string) ([]string, error) {
	// Use sh -c with ls to expand the glob. Pattern must be passed unquoted
	// for glob expansion to work; this is safe because ls -d only reads metadata.
	out, err := c.podExec(nil, "sh", "-c", fmt.Sprintf("ls -d %s 2>/dev/null || true", pattern))
	if err != nil {
		return nil, err
	}
	output := strings.TrimSpace(string(out))
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

// shellQuote wraps a string in single quotes for safe shell expansion.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Exists checks if a path exists in the pod.
func (c *K8sConnection) Exists(path string) (bool, error) {
	_, err := c.podExec(nil, "test", "-e", path)
	if err != nil {
		// test -e exits non-zero if path doesn't exist
		var connErr *ConnectionError
		if ok := isConnectionError(err, &connErr); ok {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// isConnectionError checks if an error is a ConnectionError (non-network failure from test/stat).
func isConnectionError(err error, target **ConnectionError) bool {
	if ce, ok := err.(*ConnectionError); ok {
		*target = ce
		return true
	}
	return false
}

// Exec runs a command in the pod.
func (c *K8sConnection) Exec(cmd string, args ...string) ([]byte, error) {
	return c.podExec(nil, cmd, args...)
}

// ExecDir runs a command in a specific directory in the pod.
func (c *K8sConnection) ExecDir(dir, cmd string, args ...string) ([]byte, error) {
	// Wrap in sh -c with cd, quoting arguments to prevent injection
	shellCmd := fmt.Sprintf("cd %s && %s", shellQuote(dir), shellQuote(cmd))
	for _, a := range args {
		shellCmd += " " + shellQuote(a)
	}
	return c.podExec(nil, "sh", "-c", shellCmd)
}

// ExecEnv runs a command with environment variables in the pod.
func (c *K8sConnection) ExecEnv(env map[string]string, cmd string, args ...string) ([]byte, error) {
	// Use env command to set variables
	envArgs := []string{}
	for k, v := range env {
		envArgs = append(envArgs, k+"="+v)
	}
	envArgs = append(envArgs, cmd)
	envArgs = append(envArgs, args...)
	return c.podExec(nil, "env", envArgs...)
}

// Verify K8sConnection implements Connection.
var _ Connection = (*K8sConnection)(nil)
