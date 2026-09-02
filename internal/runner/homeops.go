package runner

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

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
		n.touched(op)
		entry, err := tree.Mkdir(op.Path)
		n.answer(ctx, t, id, HomeResult{Entry: &entry}, err)
	case OpRemove:
		n.touched(op)
		n.answer(ctx, t, id, HomeResult{}, tree.Remove(op.Path))
	case OpMove:
		n.touched(op)
		entry, err := tree.Move(op.Path, op.To)
		n.answer(ctx, t, id, HomeResult{Entry: &entry}, err)
	case OpWrite:
		n.touched(op)
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

// touched marks the working copy as changed before a browser operation
// modifies it. A copy that was exactly its snapshot is not any more, and the
// mark is what keeps the next wake from pruning the upload as "not in the
// snapshot" when the sync the control plane owes it did not go through (#153).
// The control plane's own dirty flag covers the same window from its side; the
// mark here is the one that survives a control-plane restart.
func (n *Node) touched(op HomeOp) {
	home, _, _ := n.Docker.AgentHome(op.AgentID)
	if homestore.SyncedHash(home) != "" {
		homestore.MarkInUse(home)
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
	n.pump(ctx, t, id, op.Window, rc)
}

// streamZip plans afresh and writes the archive into the link. Planning twice
// is cheaper than carrying the file list of a whole home through the control
// channel — and the measurement the other side already has is what decided
// whether this is allowed to start at all.
func (n *Node) streamZip(ctx context.Context, t Transport, id string, tree *sandboxfs.FS, op HomeOp) {
	plan, err := tree.PlanZip(op.Paths)
	if err != nil {
		// With EOF: the other side opened this as a stream and reads until the
		// message that ends it. An error without EOF left it waiting for a
		// next chunk that never came (#156).
		n.replyHome(ctx, t, id, HomeResult{Err: err.Error(), ErrKind: sandboxfs.ErrorKind(err), EOF: true}, true)
		return
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tree.WriteZip(pw, plan)) }()
	defer pr.Close()
	n.pump(ctx, t, id, op.Window, pr)
}

// stream is a chunked answer under way: the credits it may still spend, and
// whether the reader has gone.
type stream struct {
	mu        sync.Mutex
	credit    int
	cancelled bool
	wake      chan struct{}
}

// openStream registers a stream with its initial window. window 0 = no flow
// control asked for; the stream then never waits (the behaviour before #156).
func (n *Node) openStream(id string, window int) *stream {
	st := &stream{credit: window, wake: make(chan struct{}, 1)}
	if window <= 0 {
		st.credit = -1 // unlimited
	}
	n.mu.Lock()
	n.streams[id] = st
	n.mu.Unlock()
	return st
}

func (n *Node) closeStream(id string) {
	n.mu.Lock()
	delete(n.streams, id)
	n.mu.Unlock()
}

// grant hands a stream credits, or cancels it.
func (n *Node) grant(id string, chunks int) {
	n.mu.Lock()
	st := n.streams[id]
	n.mu.Unlock()
	if st == nil {
		return // already over
	}
	st.mu.Lock()
	if chunks < 0 {
		st.cancelled = true
	} else if st.credit >= 0 {
		st.credit += chunks
	}
	st.mu.Unlock()
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

// take spends one credit, waiting for it if none is left. False = the stream
// is over: cancelled by the reader, or nobody granted anything for as long as
// a home operation may take — a reader that vanished without saying so.
func (st *stream) take(ctx context.Context) bool {
	for {
		st.mu.Lock()
		switch {
		case st.cancelled:
			st.mu.Unlock()
			return false
		case st.credit < 0:
			st.mu.Unlock()
			return true
		case st.credit > 0:
			st.credit--
			st.mu.Unlock()
			return true
		}
		st.mu.Unlock()
		select {
		case <-st.wake:
		case <-ctx.Done():
			return false
		case <-time.After(homeOpTimeout):
			return false
		}
	}
}

// pump moves a stream into the link, chunk by chunk, and closes it with EOF.
//
// With a window it waits for the reader: the control plane's read loop
// delivers chunks into a bounded channel, and a runner that sent faster than
// the browser read blocked that loop — and with it every other answer on the
// connection, until the host counted as not answering and a start under way on
// it was taken back (#156). Now at most `window` chunks are in flight; the
// reader grants more as it consumes.
func (n *Node) pump(ctx context.Context, t Transport, id string, window int, r io.Reader) {
	st := n.openStream(id, window)
	defer n.closeStream(id)
	buf := make([]byte, chunkLimit)
	for {
		if !st.take(ctx) {
			n.Log.Debug("runner: stream ended by the reader", "id", id)
			return
		}
		read, err := r.Read(buf)
		if read > 0 {
			chunk := make([]byte, read)
			copy(chunk, buf[:read])
			if sendErr := n.sendHome(ctx, t, id, HomeResult{Data: chunk}); sendErr != nil {
				// The link is gone. Reading the rest of a 4 GB file to send it
				// nowhere is the one thing not worth doing here.
				return
			}
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

// sendHome is replyHome for a chunk: the caller wants to know when the link is
// gone, because it decides whether to keep reading.
func (n *Node) sendHome(ctx context.Context, t Transport, id string, res HomeResult) error {
	msg, err := encode(TypeHomeResult, id, res)
	if err != nil {
		return err
	}
	return n.send(ctx, t, msg)
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
