package web

import (
	"os/exec"
	"testing"
)

// The panel merges pasted Xray configs in the browser (static/xray.js);
// its checks run under node when node is installed.
func TestXrayMerge(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "xray_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
