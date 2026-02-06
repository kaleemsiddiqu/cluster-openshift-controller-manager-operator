package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/component-base/cli"

	otecmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	oteextension "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/openshift/cluster-openshift-controller-manager-operator/pkg/version"

	_ "github.com/openshift/cluster-openshift-controller-manager-operator/test/e2e"

	"k8s.io/klog/v2"
)

func main() {
	command := newOperatorTestCommand(context.Background())
	code := cli.Run(command)
	os.Exit(code)
}

func newOperatorTestCommand(ctx context.Context) *cobra.Command {
	registry := prepareOperatorTestsRegistry()

	cmd := &cobra.Command{
		Use:   "cluster-openshift-controller-manager-operator-tests-ext",
		Short: "A binary used to run cluster-openshift-controller-manager-operator tests as part of OTE.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cmd.Help(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	if v := version.Get().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(otecmd.DefaultExtensionCommands(registry)...)

	return cmd
}

func prepareOperatorTestsRegistry() *oteextension.Registry {
	registry := oteextension.NewRegistry()
	extension := oteextension.NewExtension("openshift", "payload", "cluster-openshift-controller-manager-operator")

	// Build test specs from Ginkgo tests
	testSpecs, err := oteginkgo.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		klog.Fatalf("failed to build test specs: %v", err)
	}

	testTimeout := 30 * time.Minute

	// Register serial test suite for tests that must run serially
	serialSuite := oteextension.Suite{
		Name: "openshift/cluster-openshift-controller-manager-operator/operator/serial",
		Qualifiers: []string{
			`test.Name.Contains("[Serial]") && (test.Name.Contains("[Operator]") || test.Name.Contains("[TLS]") || test.Name.Contains("[Build]") || test.Name.Contains("[Image]"))`,
		},
		Parallelism: 1,
		TestTimeout: &testTimeout,
	}

	extension.AddSuite(serialSuite)
	extension.AddSpecs(testSpecs)

	registry.Register(extension)
	return registry
}
