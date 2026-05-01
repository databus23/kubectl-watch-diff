package tests

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/databus23/kubectl-diff-watch/pkg/diff"
	"github.com/databus23/kubectl-diff-watch/pkg/watch"
)

var (
	testEnv    *envtest.Environment
	restConfig *rest.Config
	clientset  *kubernetes.Clientset
)

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{}

	var err error
	restConfig, err = testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start envtest: %v\n", err)
		os.Exit(1)
	}

	clientset, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create clientset: %v\n", err)
		testEnv.Stop() //nolint:errcheck
		os.Exit(1)
	}

	code := m.Run()

	testEnv.Stop() //nolint:errcheck
	os.Exit(code)
}


func TestWatchConfigMapChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	// Create the initial ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm",
			Namespace: ns,
		},
		Data: map[string]string{
			"key1": "value1",
		},
	}
	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	// Set up the watcher
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Start watching in background
	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmap", "test-cm"}) //nolint:errcheck
	}()

	// Wait for informer to sync
	time.Sleep(2 * time.Second)

	// Update the ConfigMap
	cm.Data["key1"] = "value1-updated"
	cm.Data["key2"] = "value2"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update configmap: %v", err)
	}

	// Wait for the diff to appear
	waitForOutput(t, &buf, "value1-updated", 10*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Verify the diff contains expected changes
	assertContains(t, output, "-   key1: value1")
	assertContains(t, output, "+   key1: value1-updated")
	assertContains(t, output, "+   key2: value2")
	assertContains(t, output, "changed")
}

func TestWatchConfigMapDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	// Create the ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm-delete",
			Namespace: ns,
		},
		Data: map[string]string{
			"hello": "world",
		},
	}
	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	// Set up the watcher
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmap", "test-cm-delete"}) //nolint:errcheck
	}()

	// Wait for informer to sync
	time.Sleep(2 * time.Second)

	// Delete the ConfigMap
	err = clientset.CoreV1().ConfigMaps(ns).Delete(ctx, "test-cm-delete", metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("failed to delete configmap: %v", err)
	}

	// Wait for the delete event
	waitForOutput(t, &buf, "deleted", 10*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	assertContains(t, output, "deleted")
	assertContains(t, output, "-   hello: world")
}

func TestWatchWithLabelSelector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	// Create two configmaps, only one matches label selector
	cmMatching := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cm-matching",
			Namespace: ns,
			Labels:    map[string]string{"app": "test"},
		},
		Data: map[string]string{"key": "original"},
	}
	cmNonMatching := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cm-nonmatching",
			Namespace: ns,
			Labels:    map[string]string{"app": "other"},
		},
		Data: map[string]string{"key": "original"},
	}

	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cmMatching, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create matching configmap: %v", err)
	}
	_, err = clientset.CoreV1().ConfigMaps(ns).Create(ctx, cmNonMatching, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create non-matching configmap: %v", err)
	}

	// Set up the watcher with label selector
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
		LabelSelector:      "app=test",
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmaps"}) //nolint:errcheck
	}()

	// Wait for informer to sync
	time.Sleep(2 * time.Second)

	// Update both
	cmNonMatching.Data["key"] = "should-not-appear"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cmNonMatching, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update non-matching configmap: %v", err)
	}

	cmMatching.Data["key"] = "updated-value"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cmMatching, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update matching configmap: %v", err)
	}

	// Wait for the matching change to appear
	waitForOutput(t, &buf, "updated-value", 10*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Should see the matching configmap's change
	assertContains(t, output, "updated-value")
	// Should NOT see the non-matching configmap's change
	assertNotContains(t, output, "should-not-appear")
}

func TestWatchColorOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm-color",
			Namespace: ns,
		},
		Data: map[string]string{"color": "red"},
	}
	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	// Use color output
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, false)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmap", "test-cm-color"}) //nolint:errcheck
	}()

	time.Sleep(2 * time.Second)

	cm.Data["color"] = "blue"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update configmap: %v", err)
	}

	waitForOutput(t, &buf, "blue", 10*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Color output should contain ANSI escape sequences
	assertContains(t, output, "\033[")
	// And the actual content
	assertContains(t, output, "blue")
	assertContains(t, output, "red")
}

func TestWatchDyffOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm-dyff",
			Namespace: ns,
		},
		Data: map[string]string{"foo": "bar"},
	}
	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	// Use dyff output
	var buf syncBuffer

	differ, _ := diff.New("dyff", 3, false)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmap", "test-cm-dyff"}) //nolint:errcheck
	}()

	time.Sleep(2 * time.Second)

	cm.Data["foo"] = "baz"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update configmap: %v", err)
	}

	waitForOutput(t, &buf, "baz", 10*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// dyff output shows structural changes — should mention the data.foo path
	assertContains(t, output, "data")
	assertContains(t, output, "foo")
	// Should show old and new values
	assertContains(t, output, "bar")
	assertContains(t, output, "baz")
}

func TestWatchStripManagedFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm-managed",
			Namespace: ns,
		},
		Data: map[string]string{"x": "1"},
	}
	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	// Watch WITHOUT stripping managed fields
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: false,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmap", "test-cm-managed"}) //nolint:errcheck
	}()

	time.Sleep(2 * time.Second)

	cm.Data["x"] = "2"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update configmap: %v", err)
	}

	waitForOutput(t, &buf, "x: \"2\"", 10*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// With strip-managed=false, managedFields content should appear in diff
	assertContains(t, output, "manager:")
	assertContains(t, output, "operation: Update")
}

func TestWatchMultipleResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	// Create a configmap and a secret
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-cm",
			Namespace: ns,
			Labels:    map[string]string{"multi": "true"},
		},
		Data: map[string]string{"cm-key": "cm-val"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-secret",
			Namespace: ns,
			Labels:    map[string]string{"multi": "true"},
		},
		StringData: map[string]string{"secret-key": "secret-val"},
	}

	_, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}
	_, err = clientset.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create secret: %v", err)
	}

	// Watch both resource types
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
		LabelSelector:      "multi=true",
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmaps,secrets"}) //nolint:errcheck
	}()

	time.Sleep(2 * time.Second)

	// Update the configmap
	cm.Data["cm-key"] = "cm-val-updated"
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update configmap: %v", err)
	}

	waitForOutput(t, &buf, "cm-val-updated", 10*time.Second)

	// Update the secret
	secret.StringData = map[string]string{"secret-key": "secret-val-updated"}
	_, err = clientset.CoreV1().Secrets(ns).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update secret: %v", err)
	}

	waitForOutput(t, &buf, "c2VjcmV0LXZhbC11cGRhdGVk", 10*time.Second) // base64("secret-val-updated")

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	assertContains(t, output, "cm-val-updated")
	assertContains(t, output, "c2VjcmV0LXZhbC11cGRhdGVk") // base64("secret-val-updated")
}

func TestWatchNoChangeOnSameContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := createTestNamespace(t, ctx)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm-nochange",
			Namespace: ns,
		},
		Data: map[string]string{"stable": "value"},
	}
	created, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: ns}, watch.Options{
		StripManagedFields: true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"configmap", "test-cm-nochange"}) //nolint:errcheck
	}()

	time.Sleep(2 * time.Second)

	// Update with same data (only metadata/resourceVersion changes)
	created.Data["stable"] = "value" // same
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, created, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update configmap: %v", err)
	}

	// Wait a moment to ensure no diff appears
	time.Sleep(3 * time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Should NOT contain "changed" since content is identical after stripping
	assertNotContains(t, output, "changed")
}

func TestWatchClusterScopedResource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a namespace to watch (namespaces are cluster-scoped)
	nsName := fmt.Sprintf("test-cluster-scoped-%d", time.Now().UnixNano())
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nsName,
			Labels: map[string]string{"test": "cluster-scoped"},
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}
	t.Cleanup(func() {
		clientset.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{}) //nolint:errcheck
	})

	// Set up the watcher with a namespace configured (simulates user having a
	// namespace set in kubeconfig context). This should NOT affect cluster-scoped
	// resource watching.
	var buf syncBuffer

	differ, _ := diff.New("diff", 3, true)
	w, err := watch.New(watch.Config{RestConfig: restConfig, Namespace: "some-ns"}, watch.Options{
		StripManagedFields: true,
		StripServerFields:  true,
		ShowTimestamps:     false,
		Differ:             differ,
		Output:             &buf,
	})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(watchCtx, []string{"namespace", nsName}) //nolint:errcheck
	}()

	// Wait for watcher to start
	waitForOutput(t, &buf, "watching", 5*time.Second)

	// Update the namespace
	updated, err := clientset.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get namespace: %v", err)
	}
	updated.Labels["new-label"] = "new-value"
	_, err = clientset.CoreV1().Namespaces().Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update namespace: %v", err)
	}

	// Wait for the diff output
	waitForOutput(t, &buf, "changed", 5*time.Second)

	watchCancel()
	wg.Wait()

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Should show the namespace change without errors
	assertContains(t, output, "Namespace/"+nsName+" changed")
	assertContains(t, output, "+     new-label: new-value")
	// Should NOT contain the configured namespace in the watching line
	// (cluster-scoped resources don't use namespace)
	assertNotContains(t, output, "some-ns")
}

// --- Helpers ---

func createTestNamespace(t *testing.T, ctx context.Context) string {
	t.Helper()
	ns := fmt.Sprintf("test-%d", time.Now().UnixNano())
	_, err := clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}
	t.Cleanup(func() {
		clientset.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) //nolint:errcheck
	})
	return ns
}

// syncBuffer is a thread-safe buffer for capturing watcher output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForOutput(t *testing.T, buf *syncBuffer, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in output. Got:\n%s", substr, buf.String())
}

func assertContains(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("expected output to contain %q, but it didn't.\nOutput:\n%s", substr, output)
	}
}

func assertNotContains(t *testing.T, output, substr string) {
	t.Helper()
	if strings.Contains(output, substr) {
		t.Errorf("expected output NOT to contain %q, but it did.\nOutput:\n%s", substr, output)
	}
}
