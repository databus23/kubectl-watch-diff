package watch

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mgutz/ansi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/databus23/kubectl-diff-watch/pkg/diff"
)

// Config holds the Kubernetes connection configuration.
type Config struct {
	Kubeconfig string
	Context    string
	Namespace  string
	// RestConfig allows passing a pre-built rest.Config (e.g., for testing).
	// If set, Kubeconfig and Context are ignored.
	RestConfig *rest.Config
}

// Options configures the watcher behavior.
type Options struct {
	StripManagedFields bool
	StripServerFields  bool
	NoColor            bool
	ShowTimestamps     bool
	Differ             diff.Differ
	Output             io.Writer
	LabelSelector      string
	FieldSelector      string
	AllNamespaces      bool
}

// Watcher watches Kubernetes resources and diffs changes.
type Watcher struct {
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	namespace       string
	allNamespaces   bool
	labelSelector   string
	fieldSelector   string
	opts            Options
}

// New creates a new Watcher from the given config.
func New(cfg Config, opts Options) (*Watcher, error) {
	restConfig, err := buildRESTConfig(cfg)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	namespace := cfg.Namespace
	allNamespaces := opts.AllNamespaces

	labelSelector := ""
	if opts.LabelSelector != "" {
		labelSelector = opts.LabelSelector
	}

	fieldSelector := ""
	if opts.FieldSelector != "" {
		fieldSelector = opts.FieldSelector
	}

	return &Watcher{
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
		namespace:       namespace,
		allNamespaces:   allNamespaces,
		labelSelector:   labelSelector,
		fieldSelector:   fieldSelector,
		opts:            opts,
	}, nil
}

func buildRESTConfig(cfg Config) (*rest.Config, error) {
	if cfg.RestConfig != nil {
		return cfg.RestConfig, nil
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if cfg.Kubeconfig != "" {
		loadingRules.ExplicitPath = cfg.Kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if cfg.Context != "" {
		configOverrides.CurrentContext = cfg.Context
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	return kubeConfig.ClientConfig()
}

// Run starts watching the specified resources.
// args follow kubectl conventions: [resource] or [resource] [name] or [resource/name]
func (w *Watcher) Run(ctx context.Context, args []string) error {
	resources, err := w.parseArgs(args)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, res := range resources {
		wg.Add(1)
		go func(r resourceRef) {
			defer wg.Done()
			w.watchResource(ctx, r)
		}(res)
	}

	// Wait for context cancellation
	<-ctx.Done()
	wg.Wait()
	return nil
}

type resourceRef struct {
	gvr        schema.GroupVersionResource
	name       string
	namespace  string
	namespaced bool
}

func (w *Watcher) parseArgs(args []string) ([]resourceRef, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no resources specified")
	}

	var refs []resourceRef

	// First arg is the resource type (possibly comma-separated), second (optional) is name
	resourceArg := args[0]
	name := ""
	if len(args) > 1 {
		name = args[1]
	}

	// Handle resource/name syntax
	if strings.Contains(resourceArg, "/") && name == "" {
		parts := strings.SplitN(resourceArg, "/", 2)
		resourceArg = parts[0]
		name = parts[1]
	}

	// Handle comma-separated resource types
	resourceTypes := strings.Split(resourceArg, ",")

	for _, rt := range resourceTypes {
		gvr, namespaced, err := w.resolveResource(rt)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve resource %q: %w", rt, err)
		}

		ns := ""
		if namespaced {
			if w.allNamespaces {
				ns = metav1.NamespaceAll
			} else {
				ns = w.namespace
			}
		}

		refs = append(refs, resourceRef{
			gvr:        gvr,
			name:       name,
			namespace:  ns,
			namespaced: namespaced,
		})
	}

	return refs, nil
}

func (w *Watcher) resolveResource(resource string) (schema.GroupVersionResource, bool, error) {
	// Normalize: lowercase, handle plurals
	resource = strings.ToLower(resource)

	apiResources, err := w.discoveryClient.ServerPreferredResources()
	if err != nil {
		// Discovery may return partial results with an error
		if apiResources == nil {
			return schema.GroupVersionResource{}, false, fmt.Errorf("failed to discover API resources: %w", err)
		}
	}

	for _, list := range apiResources {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			// Match by name or short name
			if matchesResource(resource, r) {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				}, r.Namespaced, nil
			}
		}
	}

	return schema.GroupVersionResource{}, false, fmt.Errorf("resource %q not found on the server", resource)
}

func matchesResource(input string, r metav1.APIResource) bool {
	if input == r.Name || input == r.SingularName {
		return true
	}
	for _, shortName := range r.ShortNames {
		if input == shortName {
			return true
		}
	}
	return false
}

func (w *Watcher) watchResource(ctx context.Context, ref resourceRef) {
	tweakFunc := func(options *metav1.ListOptions) {
		if ref.name != "" {
			options.FieldSelector = "metadata.name=" + ref.name
		}
		if w.fieldSelector != "" {
			if options.FieldSelector != "" {
				options.FieldSelector += ","
			}
			options.FieldSelector += w.fieldSelector
		}
		if w.labelSelector != "" {
			options.LabelSelector = w.labelSelector
		}
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		w.dynamicClient,
		0, // no resync
		ref.namespace,
		tweakFunc,
	)

	informer := factory.ForResource(ref.gvr)

	tracker := &objectTracker{
		objects: make(map[string]*trackedObject),
		watcher: w,
		ref:     ref,
	}

	informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{ //nolint:errcheck
		AddFunc: func(obj interface{}) {
			tracker.handleEvent("ADDED", obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			tracker.handleEvent("MODIFIED", newObj)
		},
		DeleteFunc: func(obj interface{}) {
			tracker.handleEvent("DELETED", obj)
		},
	})

	resourceDesc := ref.gvr.Resource
	if ref.namespace != "" {
		resourceDesc = ref.namespace + "/" + resourceDesc
	}
	if ref.name != "" {
		resourceDesc += "/" + ref.name
	}
	fmt.Fprintf(w.opts.Output, "%s\n", //nolint:errcheck
		w.colorize(fmt.Sprintf("watching %s ...", resourceDesc), "blue+b"))

	informer.Informer().Run(ctx.Done())
}

type trackedObject struct {
	resourceVersion string
	yaml            string
}

type objectTracker struct {
	mu      sync.Mutex
	objects map[string]*trackedObject
	watcher *Watcher
	ref     resourceRef
}

func (t *objectTracker) handleEvent(eventType string, obj interface{}) {
	unstr, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := objectKey(unstr)
	currentRV := unstr.GetResourceVersion()

	// Clean the object for diffing
	cleaned := t.watcher.cleanObject(unstr)

	yamlBytes, err := yaml.Marshal(cleaned)
	if err != nil {
		return
	}
	currentYAML := string(yamlBytes)

	prev, exists := t.objects[key]

	switch eventType {
	case "ADDED":
		t.objects[key] = &trackedObject{
			resourceVersion: currentRV,
			yaml:            currentYAML,
		}
		// Don't diff on initial add
		return

	case "MODIFIED":
		if exists && prev.resourceVersion == currentRV {
			return // No change
		}
		if exists && prev.yaml == currentYAML {
			// ResourceVersion changed but content is the same after cleaning
			t.objects[key] = &trackedObject{
				resourceVersion: currentRV,
				yaml:            currentYAML,
			}
			return
		}

		header := t.formatHeader(unstr)
		if t.watcher.opts.ShowTimestamps {
			fmt.Fprintf(t.watcher.opts.Output, "\n%s %s\n", //nolint:errcheck
				t.watcher.colorize(time.Now().Format("15:04:05"), "white+b"),
				t.watcher.colorize(header+" changed", "yellow+b"))
		} else {
			fmt.Fprintf(t.watcher.opts.Output, "\n%s\n", //nolint:errcheck
				t.watcher.colorize(header+" changed", "yellow+b"))
		}

		oldYAML := ""
		if exists {
			oldYAML = prev.yaml
		}
		if err := t.watcher.opts.Differ.Diff(t.watcher.opts.Output, header, oldYAML, currentYAML); err != nil {
			fmt.Fprintf(t.watcher.opts.Output, "error computing diff: %v\n", err) //nolint:errcheck
		}

		t.objects[key] = &trackedObject{
			resourceVersion: currentRV,
			yaml:            currentYAML,
		}

	case "DELETED":
		header := t.formatHeader(unstr)
		if t.watcher.opts.ShowTimestamps {
			fmt.Fprintf(t.watcher.opts.Output, "\n%s %s\n", //nolint:errcheck
				t.watcher.colorize(time.Now().Format("15:04:05"), "white+b"),
				t.watcher.colorize(header+" deleted", "red+b"))
		} else {
			fmt.Fprintf(t.watcher.opts.Output, "\n%s\n", //nolint:errcheck
				t.watcher.colorize(header+" deleted", "red+b"))
		}

		if exists {
			if err := t.watcher.opts.Differ.Diff(t.watcher.opts.Output, header, prev.yaml, ""); err != nil {
				fmt.Fprintf(t.watcher.opts.Output, "error computing diff: %v\n", err) //nolint:errcheck
			}
		}

		delete(t.objects, key)
	}
}

func (t *objectTracker) formatHeader(obj *unstructured.Unstructured) string {
	gvk := obj.GroupVersionKind()
	kind := gvk.Kind
	if kind == "" {
		kind = t.ref.gvr.Resource
	}
	ns := obj.GetNamespace()
	name := obj.GetName()

	if ns != "" {
		return fmt.Sprintf("%s/%s -n %s", kind, name, ns)
	}
	return fmt.Sprintf("%s/%s", kind, name)
}

func objectKey(obj *unstructured.Unstructured) string {
	ns := obj.GetNamespace()
	name := obj.GetName()
	if ns != "" {
		return ns + "/" + name
	}
	return name
}

func (w *Watcher) cleanObject(obj *unstructured.Unstructured) map[string]interface{} {
	// Deep copy to avoid mutating the cache
	cleaned := obj.DeepCopy().Object

	if w.opts.StripManagedFields {
		stripManagedFields(cleaned)
	}

	if w.opts.StripServerFields {
		stripServerFields(cleaned)
	}

	return cleaned
}

func stripManagedFields(obj map[string]interface{}) {
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		delete(metadata, "managedFields")
	}
}

// stripServerFields removes fields that are set/updated by the server on every
// write and produce noisy diffs without carrying meaningful information.
func stripServerFields(obj map[string]interface{}) {
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		delete(metadata, "resourceVersion")
		delete(metadata, "generation")
	}
	if status, ok := obj["status"].(map[string]interface{}); ok {
		delete(status, "observedGeneration")
	}
}

func (w *Watcher) colorize(text, color string) string {
	if w.opts.NoColor {
		return text
	}
	return ansi.Color(text, color)
}
