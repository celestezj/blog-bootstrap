package workspace

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/state"
)

func TestChangesRecordsEveryMutation(t *testing.T) {
	root := t.TempDir()
	ctx := &core.Context{
		Root:  root,
		State: state.New(root+"/.bootstrap/state.json", nil),
	}
	err := RecordChange(ctx, func(c *state.Changes) {
		c.SystemPackages = append(c.SystemPackages, state.PackageChange{Manager: "winget", Package: "Git.Git", Action: "install"})
		c.PortableTools = append(c.PortableTools, state.ToolChange{Name: "node", Version: "22.14.0", Path: root + "/tools/node"})
		c.SSHBlocks = append(c.SSHBlocks, state.SSHChange{File: root + "/.ssh/config", Alias: "celestezj", Block: "managed"})
		c.Backups = append(c.Backups, root+"/.ssh/config.bak-1")
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ctx.ChangesPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc ChangesDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != state.SchemaVersion {
		t.Fatalf("schema = %d", doc.SchemaVersion)
	}
	if len(doc.SystemPackages) != 1 || doc.SystemPackages[0].Package != "Git.Git" {
		t.Fatalf("packages = %+v", doc.SystemPackages)
	}
	if len(doc.PortableTools) != 1 || doc.PortableTools[0].Name != "node" {
		t.Fatalf("tools = %+v", doc.PortableTools)
	}
	if len(doc.SSHBlocks) != 1 || doc.SSHBlocks[0].Alias != "celestezj" {
		t.Fatalf("ssh = %+v", doc.SSHBlocks)
	}
	if len(doc.Backups) != 1 {
		t.Fatalf("backups = %+v", doc.Backups)
	}
}
