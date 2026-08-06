package upload

import (
	"fmt"
	"strings"
	"testing"
)

func TestGHAuthToken(t *testing.T) {
	tests := []struct {
		name      string
		out       string
		runErr    error
		want      string
		wantInErr string
	}{
		{name: "trims trailing newline", out: "gho_abc123\n", want: "gho_abc123"},
		{name: "empty output", out: "  \n", wantInErr: "returned no token"},
		{name: "gh failure", runErr: fmt.Errorf("exit status 1"), wantInErr: "gh auth token: exit status 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			token, err := ghAuthToken(func(name string, args ...string) ([]byte, error) {
				gotName, gotArgs = name, args
				return []byte(tc.out), tc.runErr
			})
			if gotName != "gh" || strings.Join(gotArgs, " ") != "auth token" {
				t.Errorf("ran %q %v, want gh auth token", gotName, gotArgs)
			}
			if tc.wantInErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantInErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if token != tc.want {
				t.Errorf("token = %q, want %q", token, tc.want)
			}
		})
	}
}
