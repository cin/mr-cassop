package nodectl

import (
	"errors"
	"testing"
)

func TestClusterViewOwnsTokens(t *testing.T) {
	view := ClusterView{
		LiveNodes: []string{"10.0.0.1"},
		// 10.0.0.2 is down, 10.0.0.9 left the ring: both show up as unreachable
		UnreachableNodes: []string{"10.0.0.2", "10.0.0.9"},
		LeavingNodes:     []string{"10.0.0.3"},
		EndpointToHostId: map[string]string{
			"10.0.0.1": "host-1",
			"10.0.0.2": "host-2",
			"10.0.0.3": "host-3",
		},
	}

	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.0.0.2", true}, // down, but still owns tokens
		{"10.0.0.3", true}, // leaving, still owns tokens until it's done
		{"10.0.0.9", false},
	}
	for _, tt := range tests {
		got, err := view.OwnsTokens(tt.ip)
		if err != nil {
			t.Fatalf("OwnsTokens(%q) returned %v", tt.ip, err)
		}
		if got != tt.want {
			t.Errorf("OwnsTokens(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

// Without token metadata, OwnsTokens must fail instead of reporting every node as gone.
func TestClusterViewOwnsTokensWithoutTokenMetadata(t *testing.T) {
	_, err := ClusterView{LiveNodes: []string{"10.0.0.1"}}.OwnsTokens("10.0.0.9")
	if !errors.Is(err, ErrNoTokenMetadata) {
		t.Fatalf("err = %v, want ErrNoTokenMetadata", err)
	}
}
