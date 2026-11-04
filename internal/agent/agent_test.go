package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuideMatchesDocs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "agent-install.md"))
	if err != nil {
		t.Fatalf("read docs guide: %v", err)
	}
	if string(data) != Guide() {
		t.Fatal("agent-guide output drifted from docs/agent-install.md; update internal/agent/guide.md")
	}
}
