package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/databus23/kubectl-diff-watch/pkg/diff"
	"github.com/databus23/kubectl-diff-watch/pkg/watch"
)

var (
	version = "dev"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type options struct {
	kubeconfig         string
	context            string
	namespace          string
	outputFormat       string
	contextLines       int
	noColor            bool
	showManagedFields  bool
	stripServer        bool
	showTimestamps     bool
	labelSelector      string
	fieldSelector      string
	allNamespaces      bool
}

func newRootCmd() *cobra.Command {
	o := &options{}

	cmd := &cobra.Command{
		Use:   "kubectl-diff-watch [resource] [name]",
		Short: "Watch Kubernetes resources and show diffs when they change",
		Long: `Watch one or more Kubernetes resources and display a colored diff
whenever a resource changes. Supports multiple output formats including
colored unified diff and dyff structural output.

Works as a kubectl plugin: kubectl diff-watch <resource> [name]`,
		Example: `  # Watch a specific pod
  kubectl diff-watch pod mypod

  # Watch all pods with a label
  kubectl diff-watch pods -l app=nginx

  # Watch a deployment in a specific namespace
  kubectl diff-watch deployment myapp -n production

  # Watch nodes with dyff output
  kubectl diff-watch nodes -o dyff

  # Watch multiple resource types
  kubectl diff-watch pods,deployments -l app=nginx

  # Watch including managedFields
  kubectl diff-watch pod mypod --show-managed-fields`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("you must specify a resource type (e.g., pods, deployments, nodes)")
			}
			return run(cmd.Context(), o, args)
		},
		Version: version,
	}

	flags := cmd.Flags()
	flags.StringVar(&o.kubeconfig, "kubeconfig", "", "Path to the kubeconfig file (also $KUBECONFIG)")
	flags.StringVar(&o.context, "context", "", "Kubernetes context to use (also $KUBECONTEXT)")
	flags.StringVarP(&o.namespace, "namespace", "n", "", "Namespace to watch in (also $KUBENAMESPACE)")
	flags.StringVarP(&o.outputFormat, "output", "o", "diff", "Output format: diff, dyff")
	flags.IntVarP(&o.contextLines, "diff-context", "C", 3, "Number of context lines in diff output")
	flags.BoolVar(&o.noColor, "no-color", false, "Disable colored output")
	flags.BoolVar(&o.showManagedFields, "show-managed-fields", false, "If true, keep the managedFields when printing objects")
	flags.BoolVar(&o.stripServer, "strip-server", true, "Strip resourceVersion, generation, observedGeneration from output")
	flags.BoolVar(&o.showTimestamps, "timestamps", true, "Show timestamps on each diff")
	flags.StringVarP(&o.labelSelector, "selector", "l", "", "Label selector to filter resources")
	flags.StringVar(&o.fieldSelector, "field-selector", "", "Field selector to filter resources")
	flags.BoolVarP(&o.allNamespaces, "all-namespaces", "A", false, "Watch resources across all namespaces")

	return cmd
}

func envDefault(val, envVar string) string {
	if val != "" {
		return val
	}
	return os.Getenv(envVar)
}

func run(ctx context.Context, o *options, args []string) error {
	differ, err := diff.New(o.outputFormat, o.contextLines, o.noColor)
	if err != nil {
		return err
	}

	w, err := watch.New(watch.Config{
		Kubeconfig: o.kubeconfig,
		Context:    envDefault(o.context, "KUBECONTEXT"),
		Namespace:  envDefault(o.namespace, "KUBENAMESPACE"),
	}, watch.Options{
		StripManagedFields: !o.showManagedFields,
		StripServerFields:  o.stripServer,
		NoColor:            o.noColor,
		ShowTimestamps:     o.showTimestamps,
		Differ:             differ,
		Output:             os.Stdout,
		LabelSelector:      o.labelSelector,
		FieldSelector:      o.fieldSelector,
		AllNamespaces:      o.allNamespaces,
	})
	if err != nil {
		return err
	}

	return w.Run(ctx, args)
}
