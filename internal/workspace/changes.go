package workspace

import (
	"encoding/json"
	"os"

	"github.com/celestezj/blog-bootstrap/internal/atomicfile"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/state"
)

// ChangesDocument is the standalone rollback record written to changes.json.
type ChangesDocument struct {
	SchemaVersion int `json:"schema_version"`
	state.Changes
}

// RecordChange appends a rollback entry and rewrites changes.json.
func RecordChange(ctx *core.Context, fn func(*state.Changes)) error {
	if err := ctx.State.AddChange(fn); err != nil {
		return protocol.Fatal("CHANGES_WRITE_FAILED", "无法更新安装记录: "+err.Error()).WithCause(err)
	}
	return WriteChanges(ctx)
}

// WriteChanges persists the current rollback record for manual undo.
func WriteChanges(ctx *core.Context) error {
	snapshot := ctx.State.Snapshot()
	doc := ChangesDocument{SchemaVersion: state.SchemaVersion, Changes: snapshot.Changes}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return protocol.Fatal("CHANGES_WRITE_FAILED", "无法序列化安装记录: "+err.Error()).WithCause(err)
	}
	if err := os.MkdirAll(ctx.BootstrapDir(), 0o755); err != nil {
		return protocol.Fatal("CHANGES_WRITE_FAILED", "无法创建 .bootstrap 目录: "+err.Error()).WithCause(err)
	}
	if err := atomicfile.Write(ctx.ChangesPath(), append(data, '\n'), 0o600); err != nil {
		return protocol.Fatal("CHANGES_WRITE_FAILED", "无法写入 changes.json: "+err.Error()).WithCause(err)
	}
	return nil
}
