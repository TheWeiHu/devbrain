package nightshift

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/version"
)

func TestCompatibleDashboardPortRequiresCurrentVersionAndData(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DEVBRAIN_DATA", data)

	tests := []struct {
		name       string
		serverData string
		serverVer  string
		want       bool
	}{
		{name: "current", serverData: data, serverVer: version.String(), want: true},
		{name: "stale version", serverData: data, serverVer: "older", want: false},
		{name: "legacy version", serverData: data, serverVer: "", want: false},
		{name: "foreign data", serverData: t.TempDir(), serverVer: version.String(), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"server":"devbrain-queue","data":%q,"pid":%d,"version":%q}`,
					tc.serverData, os.Getpid(), tc.serverVer)
			}))
			defer ts.Close()
			port := ts.Listener.Addr().(*net.TCPAddr).Port
			gotPort, got := compatibleDashboardPort(port, 1)
			if got != tc.want {
				t.Fatalf("compatible = %v, want %v (port %d)", got, tc.want, gotPort)
			}
			if got && gotPort != port {
				t.Fatalf("port = %d, want %d", gotPort, port)
			}
		})
	}
}
