package controllers

import (
	"context"
	"net"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"go.uber.org/mock/gomock"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "Client.Timeout exceeded" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

var _ net.Error = timeoutError{}

func TestReaperInitializationReportsReInitFailure(t *testing.T) {
	asserts := NewWithT(t)
	cc := pauseTestCluster()
	reconciler, mocked := newPauseTestReconciler(t, cc)

	mocked.reaper.EXPECT().ClusterExists(gomock.Any()).Return(false, timeoutError{})
	mocked.reaper.EXPECT().Clusters(gomock.Any()).Return([]string{cc.Name}, nil)
	mocked.reaper.EXPECT().DeleteCluster(gomock.Any()).Return(errors.New("schedule s1 returned 409"))

	err := reconciler.reaperInitialization(context.Background(), cc, mocked.reaper)

	asserts.Expect(err).To(HaveOccurred())
	asserts.Expect(err.Error()).To(ContainSubstring("schedule s1 returned 409"))
	asserts.Expect(err.Error()).NotTo(ContainSubstring("Client.Timeout exceeded"))
}
