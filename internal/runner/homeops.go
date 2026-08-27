package runner

import (
	"context"
	"errors"
	"io"

	"covey/internal/homestore"
	"covey/internal/sandboxfs"
)

// The runner side of file access. The operations are executed against the real
// sandboxfs on the working copy — path handling, the escape guard and the
// ownership all stay where the files are, in one implementation. What travels
// over the link is the result, not a second set of rules about paths.

// homeTree opens the working copy of one agent.
func (n *Node) homeTree(op HomeOp) (*sandboxfs.FS, error) {
	path, uid, gid := n.Docker.AgentHome(op.AgentID)
	return sandboxfs.New(path, uid, gid)
}

func (n *Node) homeOp(ctx context.Context, t Transport, id string, op HomeOp) {
	tree, err := n.homeTree(op)
	if err != nil {
		n.replyHome(ctx, t, id, HomeResult{Err: err.Error()}, true)
		return
	}

	switch op.Op {
	case OpList:
		listing, err := tree.List(op.Path)
		n.answer(ctx, t, id, HomeResult{Listing: &listing}, err)
	case OpRead:
		file, err := tree.Read(op.Path)
		n.answer(ctx, t, id, HomeResult{File: &file}, err)
	case OpUsage:
		usage := tree.Usage()
		n.answer(ctx, t, id, HomeResult{Usage: &usage}, nil)
	case OpMkdir:
		entry, err := tree.Mkdir(op.Path)
		n.answer(ctx, t, id, HomeResult{Entry: &entry}, err)
	case OpRemove:
		n.answer(ctx, t, id, HomeResult{}, tree.Remove(op.Path))
	case OpMove:
		entry, err := tree.Move(op.Path, op.To)
		n.answer(ctx, t, id, HomeResult{Entry: &entry}, err)
	case OpWrite:
		n.write(ctx, t, id, tree, op)
	case OpPlan:
		plan, err := tree.PlanZip(op.Paths)
		n.answer(ctx, t, id, HomeResult{Plan: &ZipMeasure{
			Name: plan.Name, Files: plan.Files, Bytes: plan.Bytes, Paths: plan.Paths,
		}}, err)
	case OpRestore:
		n.restore(ctx, t, id, op)
	case OpOpen:
		n.stream(ctx, t, id, tree, op)
	case OpZip:
		n.streamZip(ctx, t, id, tree, op)
	default:
		n.replyHome(ctx, t, id, HomeResult{Err: "unknown file operation " + op.Op}, true)
	}
}

// answer sends one result, with the error mapped so that the other side can
// turn it back into the status the HTTP layer already produces for it.
func (n *Node) answer(ctx context.Context, t Transport, id string, res HomeResult, err error) {
	if err != nil {
		res = HomeResult{Err: err.Error(), ErrKind: sandboxfs.ErrorKind(err)}
	}
	n.replyHome(ctx, t, id, res, true)
}

// write takes a file, in one message.
//
// Downloads are chunked, uploads are not, and the asymmetry is deliberate: a
// download is unbounded (a home may hold a 4 GB archive), an upload is capped
// at sandboxfs.MaxWriteBytes and the control plane checks that before it sends.
// Chunking it too would mean keeping a half-written file per connection —
// state that has to be cleaned up when a runner disconnects mid-upload, in
// exchange for a memory saving on a bounded transfer.
func (n *Node) write(ctx context.Context, t Transport, id string, tree *sandboxfs.FS, op HomeOp) {
	entry, err := tree.Write(op.Path, bytesReader(op.Data))
	n.answer(ctx, t, id, HomeResult{Entry: &entry}, err)
}

// restore brings the working copy back to an earlier state. A modifying action
// on somebody else's work, so it is guarded up there where the roles live; down
// here it is the same materialisation the wake does.
func (n *Node) restore(ctx context.Context, t Transport, id string, op HomeOp) {
	if n.Blobs == nil {
		n.replyHome(ctx, t, id, HomeResult{Err: "no home store configured"}, true)
		return
	}
	home, _, _ := n.Docker.AgentHome(op.AgentID)
	m, err := homestore.Load(ctx, n.Blobs, op.OrgID, op.Snapshot)
	if err == nil {
		// Der Restore räumt: hier hat ein Mensch gesagt „zurück auf diesen
		// Stand", und ein Zurück, das Neueres stehen ließe, wäre keins.
		_, err = homestore.MaterializeInto(ctx, n.Blobs, op.OrgID, home, m, true)
		if err == nil {
			homestore.MarkSynced(home, op.Snapshot)
		}
	}
	n.answer(ctx, t, id, HomeResult{}, err)
}

// stream sends a file in chunks. The first message carries the file's
// particulars, so the other side can set Content-Length before the first byte
// reaches the browser.
func (n *Node) stream(ctx context.Context, t Transport, id string, tree *sandboxfs.FS, op HomeOp) {
	rc, info, err := tree.Open(op.Path)
	if err != nil {
		n.answer(ctx, t, id, HomeResult{}, err)
		return
	}
	defer rc.Close()
	n.replyHome(ctx, t, id, HomeResult{Info: &info}, false)
	n.pump(ctx, t, id, rc)
}

// streamZip plans afresh and writes the archive into the link. Planning twice
// is cheaper than carrying the file list of a whole home through the control
// channel — and the measurement the other side already has is what decided
// whether this is allowed to start at all.
func (n *Node) streamZip(ctx context.Context, t Transport, id string, tree *sandboxfs.FS, op HomeOp) {
	plan, err := tree.PlanZip(op.Paths)
	if err != nil {
		n.answer(ctx, t, id, HomeResult{}, err)
		return
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tree.WriteZip(pw, plan)) }()
	defer pr.Close()
	n.pump(ctx, t, id, pr)
}

// pump moves a stream into the link, chunk by chunk, and closes it with EOF.
func (n *Node) pump(ctx context.Context, t Transport, id string, r io.Reader) {
	buf := make([]byte, chunkLimit)
	for {
		read, err := r.Read(buf)
		if read > 0 {
			chunk := make([]byte, read)
			copy(chunk, buf[:read])
			n.replyHome(ctx, t, id, HomeResult{Data: chunk}, false)
		}
		if errors.Is(err, io.EOF) {
			n.replyHome(ctx, t, id, HomeResult{EOF: true}, true)
			return
		}
		if err != nil {
			// The error goes into the last message rather than into a log line:
			// the other side is holding a half-written download and has to be
			// able to say why it stops.
			n.replyHome(ctx, t, id, HomeResult{Err: err.Error(), EOF: true}, true)
			return
		}
	}
}

func (n *Node) replyHome(ctx context.Context, t Transport, id string, res HomeResult, last bool) {
	_ = last // the correlation ends with EOF; kept for readability at the call sites
	n.reply(ctx, t, id, TypeHomeResult, res)
}

func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
