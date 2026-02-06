package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-openshift-controller-manager-operator/test/framework"
)

var _ = g.Describe("[sig-openshift-controller-manager] TLS Security Profile", func() {
	g.It("[Operator][TLS][Serial] should propagate Modern TLS profile from APIServer to OpenShift Controller Manager", func(ctx context.Context) {
		testTLSSecurityProfilePropagation(ctx, g.GinkgoTB())
	})
})

func testTLSSecurityProfilePropagation(ctx context.Context, t testing.TB) {
	client := framework.MustNewClientset(t, nil)

	// Make sure the operator is fully up
	framework.MustEnsureClusterOperatorStatusIsSet(t, client)

	// Get the current APIServer config
	apiServer, err := client.APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to get APIServer config")

	// Save the original TLS profile for cleanup
	originalTLSProfile := apiServer.Spec.TLSSecurityProfile

	// Modify the TLS security profile to use Modern profile
	// Modern profile uses TLS 1.3 with modern cipher suites
	apiServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
		Type:   configv1.TLSProfileModernType,
		Modern: &configv1.ModernTLSProfile{},
	}

	_, err = client.APIServers().Update(ctx, apiServer, metav1.UpdateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to update APIServer TLS profile to Modern")

	// Cleanup: restore original TLS profile and verify restoration
	g.DeferCleanup(func(ctx context.Context) {
		g.By("Restoring original TLS profile")
		apiServer, err := client.APIServers().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Error(err, "failed to get APIServer for cleanup")
			return
		}
		apiServer.Spec.TLSSecurityProfile = originalTLSProfile
		if _, err := client.APIServers().Update(ctx, apiServer, metav1.UpdateOptions{}); err != nil {
			g.GinkgoLogr.Error(err, "failed to restore original TLS profile")
			return
		}

		// Wait for operator to reconcile the restoration
		g.By("Waiting for operator to reconcile TLS profile restoration")
		err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
			co, err := client.ClusterOperators().Get(ctx, "openshift-controller-manager", metav1.GetOptions{})
			if err != nil {
				g.GinkgoLogr.Error(err, "error getting clusteroperator during cleanup")
				return false, nil
			}

			isAvailable := false
			isProgressing := true

			for _, c := range co.Status.Conditions {
				if c.Type == configv1.OperatorAvailable && c.Status == configv1.ConditionTrue {
					isAvailable = true
				}
				if c.Type == configv1.OperatorProgressing && c.Status == configv1.ConditionFalse {
					isProgressing = false
				}
			}

			if isAvailable && !isProgressing {
				g.GinkgoLogr.Info("Operator reconciliation after restoration complete")
				return true, nil
			}

			return false, nil
		})
		if err != nil {
			g.GinkgoLogr.Error(err, "operator did not complete reconciliation after restoration")
			return
		}

		// Verify TLS profile was restored (should be back to default TLS 1.2 or original setting)
		g.By("Verifying TLS profile was restored correctly")
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			cfg, err := client.OpenShiftControllerManagers().Get(ctx, "cluster", metav1.GetOptions{})
			if err != nil {
				g.GinkgoLogr.Error(err, "error getting openshift controller manager config during cleanup verification")
				return false, nil
			}

			observedConfig := map[string]interface{}{}
			if err := json.Unmarshal(cfg.Spec.ObservedConfig.Raw, &observedConfig); err != nil {
				g.GinkgoLogr.Error(err, "failed to unmarshal observed config during cleanup")
				return false, nil
			}

			// Check the restored TLS version
			minTLSVersion, found, err := unstructured.NestedString(observedConfig, "servingInfo", "minTLSVersion")
			if err != nil {
				g.GinkgoLogr.Error(err, "error reading minTLSVersion during cleanup")
				return false, nil
			}

			// If original profile was nil, expect default (typically VersionTLS12)
			// If original profile was set, it should match
			if originalTLSProfile == nil {
				// Default OpenShift TLS profile is typically TLS 1.2
				if found && minTLSVersion == "VersionTLS12" {
					g.GinkgoLogr.Info("TLS profile restored to default", "minTLSVersion", minTLSVersion)
					return true, nil
				}
				// Also accept if TLS config is removed entirely (using cluster defaults)
				if !found || minTLSVersion == "" {
					g.GinkgoLogr.Info("TLS profile restored to cluster defaults (no explicit TLS version)")
					return true, nil
				}
			} else {
				// If there was an original profile, verify it's not TLS 1.3 anymore
				if found && minTLSVersion != "VersionTLS13" {
					g.GinkgoLogr.Info("TLS profile restored from Modern", "minTLSVersion", minTLSVersion)
					return true, nil
				}
			}

			g.GinkgoLogr.Info("Waiting for TLS profile restoration to propagate", "current", minTLSVersion)
			return false, nil
		})
		if err != nil {
			g.GinkgoLogr.Error(err, "TLS profile was not properly restored in observed config")
		}
	})

	// Wait for the operator to start progressing (detecting the change)
	g.By("Waiting for operator to detect TLS profile change and start progressing")
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		co, err := client.ClusterOperators().Get(ctx, "openshift-controller-manager", metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Error(err, "error getting clusteroperator")
			return false, nil
		}
		for _, c := range co.Status.Conditions {
			if c.Type == configv1.OperatorProgressing && c.Status == configv1.ConditionTrue {
				g.GinkgoLogr.Info("Operator is now progressing", "reason", c.Reason)
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		g.GinkgoLogr.Info("Warning: operator did not start progressing within 5 minutes, continuing anyway", "error", err)
	}

	// Wait for the operator to finish progressing (reconciliation complete)
	// This typically takes 12-15 minutes for TLS changes to propagate
	g.By("Waiting for operator to complete reconciliation (may take up to 15 minutes)")
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		co, err := client.ClusterOperators().Get(ctx, "openshift-controller-manager", metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Error(err, "error getting clusteroperator")
			return false, nil
		}

		isAvailable := false
		isProgressing := true
		isDegraded := false

		for _, c := range co.Status.Conditions {
			if c.Type == configv1.OperatorAvailable && c.Status == configv1.ConditionTrue {
				isAvailable = true
			}
			if c.Type == configv1.OperatorProgressing && c.Status == configv1.ConditionFalse {
				isProgressing = false
			}
			if c.Type == configv1.OperatorDegraded && c.Status == configv1.ConditionTrue {
				isDegraded = true
			}
		}

		if isDegraded {
			g.GinkgoLogr.Info("Warning: operator is degraded")
			return false, nil
		}

		if isAvailable && !isProgressing {
			g.GinkgoLogr.Info("Operator reconciliation complete", "available", true, "progressing", false)
			return true, nil
		}

		g.GinkgoLogr.Info("Operator still reconciling", "available", isAvailable, "progressing", isProgressing)
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "operator did not complete reconciliation")

	// Now verify the TLS config was propagated to the observed config
	g.By("Verifying TLS config in observed config")
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		cfg, err := client.OpenShiftControllerManagers().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Error(err, "error getting openshift controller manager config")
			return false, nil
		}

		observed := string(cfg.Spec.ObservedConfig.Raw)

		// The Modern TLS profile should set minTLSVersion to TLS 1.3
		// We're looking for the propagated TLS settings
		hasTLSVersion := strings.Contains(observed, "\"minTLSVersion\"")
		hasCipherSuites := strings.Contains(observed, "\"cipherSuites\"")

		if !hasTLSVersion || !hasCipherSuites {
			g.GinkgoLogr.Info("TLS config not yet observed in config", "config", observed)
			return false, nil
		}

		g.GinkgoLogr.Info("TLS config successfully observed", "config", observed)

		// Additional validation: parse the observed config
		observedConfig := map[string]interface{}{}
		if err := json.Unmarshal(cfg.Spec.ObservedConfig.Raw, &observedConfig); err != nil {
			g.GinkgoLogr.Error(err, "failed to unmarshal observed config")
			return false, nil
		}

		// Verify servingInfo exists
		_, found, err := unstructured.NestedMap(observedConfig, "servingInfo")
		if err != nil || !found {
			g.GinkgoLogr.Info("servingInfo not found in observed config")
			return false, nil
		}

		// Verify minTLSVersion is set to TLS 1.3 (Modern profile)
		minTLSVersion, found, err := unstructured.NestedString(observedConfig, "servingInfo", "minTLSVersion")
		if err != nil || !found || minTLSVersion == "" {
			g.GinkgoLogr.Info("minTLSVersion not properly set", "found", found, "value", minTLSVersion)
			return false, nil
		}

		// Modern profile should use VersionTLS13 (exact string match)
		if minTLSVersion != "VersionTLS13" {
			g.GinkgoLogr.Info("minTLSVersion not VersionTLS13 yet", "got", minTLSVersion, "expected", "VersionTLS13")
			return false, nil
		}

		// Verify cipherSuites is set and contains the expected Modern profile ciphers
		cipherSuites, found, err := unstructured.NestedStringSlice(observedConfig, "servingInfo", "cipherSuites")
		if err != nil || !found || len(cipherSuites) == 0 {
			g.GinkgoLogr.Info("cipherSuites not properly set", "found", found, "count", len(cipherSuites))
			return false, nil
		}

		// Modern profile should have exactly these TLS 1.3 cipher suites
		expectedCiphers := []string{
			"TLS_AES_128_GCM_SHA256",
			"TLS_AES_256_GCM_SHA384",
			"TLS_CHACHA20_POLY1305_SHA256",
		}

		// Verify all expected ciphers are present
		cipherSet := make(map[string]bool)
		for _, cipher := range cipherSuites {
			cipherSet[cipher] = true
		}

		for _, expected := range expectedCiphers {
			if !cipherSet[expected] {
				// Don't fail immediately, keep polling
				g.GinkgoLogr.Info("expected cipher suite not found yet", "expected", expected, "got", cipherSuites)
				return false, nil
			}
		}

		g.GinkgoLogr.Info("Validated Modern TLS config", "minTLSVersion", minTLSVersion, "cipherSuites", cipherSuites)
		return true, nil
	})

	o.Expect(err).NotTo(o.HaveOccurred(), "Modern TLS security profile from APIServer was not propagated to OpenShift Controller Manager observed config")
}
