package e2e

import (
	"os"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "OpenShift Controller Manager Operator E2E Suite")
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
