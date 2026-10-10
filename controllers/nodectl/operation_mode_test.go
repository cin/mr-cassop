package nodectl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestOperationMode(t *testing.T) {
	tests := []struct {
		mode    string
		want    OperationMode
		wantErr bool
	}{
		{"NORMAL", NodeOperationModeNormal, false},
		// Cassandra 5.0's StorageService.Mode has these two beyond the original eight
		{"DECOMMISSION_FAILED", NodeOperationModeDecommissionFailed, false},
		{"JOINING_FAILED", NodeOperationModeJoiningFailed, false},
		{"SOMETHING_NEW", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"status":200,"value":{"OperationMode":"` + tt.mode + `"}}`))
			}))
			defer srv.Close()

			got, err := NewClient(srv.URL, "user", "pass", zap.NewNop().Sugar()).OperationMode(context.Background(), "10.0.0.1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("OperationMode() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("OperationMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
