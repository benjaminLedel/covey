package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"covey/internal/sandboxfs"
)

// remoteTree is an agent's home on another host, seen through the runner link.
// It satisfies sandboxfs.Tree, so the file browser cannot tell the difference —
// which is the point: a home is on a runner, and where that runner stands is
// not the interface's business.
type remoteTree struct {
	pool    *Pool
	conn    *conn
	agentID uuid.UUID
	orgID   uuid.UUID
}

// homeOpTimeout bounds a single file operation. Generous enough for a listing
// of a directory with a hundred thousand entries, short enough that a runner
// that has gone quiet does not leave the browser hanging.
const homeOpTimeout = 2 * time.Minute

func (t *remoteTree) op(op HomeOp) (HomeResult, error) {
	op.AgentID = t.agentID
	answer, err := t.conn.ask(context.Background(), TypeHomeOp, op, homeOpTimeout)
	if err != nil {
		return HomeResult{}, err
	}
	res, err := decode[HomeResult](answer)
	if err != nil {
		return HomeResult{}, err
	}
	if res.Err != "" {
		return res, sandboxfs.ErrorFromKind(res.ErrKind, res.Err)
	}
	return res, nil
}

func (t *remoteTree) List(rel string) (sandboxfs.Listing, error) {
	res, err := t.op(HomeOp{Op: OpList, Path: rel})
	if err != nil || res.Listing == nil {
		return sandboxfs.Listing{Path: rel}, err
	}
	return *res.Listing, nil
}

func (t *remoteTree) Read(rel string) (sandboxfs.File, error) {
	res, err := t.op(HomeOp{Op: OpRead, Path: rel})
	if err != nil || res.File == nil {
		return sandboxfs.File{}, err
	}
	return *res.File, nil
}

func (t *remoteTree) Usage() sandboxfs.Usage {
	res, err := t.op(HomeOp{Op: OpUsage})
	if err != nil || res.Usage == nil {
		// A usage figure nobody could fetch is reported as "no home" rather
		// than as zeroes that read like an empty disk.
		return sandboxfs.Usage{}
	}
	return *res.Usage
}

func (t *remoteTree) Mkdir(rel string) (sandboxfs.Entry, error) {
	return t.mutate(HomeOp{Op: OpMkdir, Path: rel})
}

func (t *remoteTree) Move(fromRel, toRel string) (sandboxfs.Entry, error) {
	return t.mutate(HomeOp{Op: OpMove, Path: fromRel, To: toRel})
}

func (t *remoteTree) Write(rel string, r io.Reader) (sandboxfs.Entry, error) {
	// Read with one byte of headroom: the limit has to be an answer, not a
	// truncated file that looks complete.
	data, err := io.ReadAll(io.LimitReader(r, sandboxfs.MaxWriteBytes+1))
	if err != nil {
		return sandboxfs.Entry{}, err
	}
	if int64(len(data)) > sandboxfs.MaxWriteBytes {
		return sandboxfs.Entry{}, sandboxfs.ErrTooLarge
	}
	return t.mutate(HomeOp{Op: OpWrite, Path: rel, Data: data})
}

func (t *remoteTree) Remove(rel string) error {
	_, err := t.mutate(HomeOp{Op: OpRemove, Path: rel})
	return err
}

// mutate performs a changing operation and marks the home as dirty: what the
// browser writes lives only in the runner's working copy until the next sync,
// and an agent that wakes elsewhere in the meantime would materialise a
// snapshot that does not have it. See Pool.markHomeDirty.
func (t *remoteTree) mutate(op HomeOp) (sandboxfs.Entry, error) {
	res, err := t.op(op)
	if err != nil {
		return sandboxfs.Entry{}, err
	}
	t.pool.markHomeDirty(t.conn, t.agentID, t.orgID)
	if res.Entry == nil {
		return sandboxfs.Entry{}, nil
	}
	return *res.Entry, nil
}

func (t *remoteTree) PlanZip(rels []string) (sandboxfs.ZipPlan, error) {
	res, err := t.op(HomeOp{Op: OpPlan, Paths: rels})
	if err != nil || res.Plan == nil {
		return sandboxfs.ZipPlan{}, err
	}
	return sandboxfs.ZipPlan{
		Name: res.Plan.Name, Files: res.Plan.Files, Bytes: res.Plan.Bytes, Paths: res.Plan.Paths,
	}, nil
}

func (t *remoteTree) WriteZip(w io.Writer, plan sandboxfs.ZipPlan) error {
	rc, _, err := t.stream(HomeOp{Op: OpZip, Paths: plan.Paths}, false)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}

func (t *remoteTree) Open(rel string) (io.ReadCloser, sandboxfs.FileInfo, error) {
	return t.stream(HomeOp{Op: OpOpen, Path: rel}, true)
}

// stream opens a chunked answer. withInfo=true waits for the first message,
// which carries the file's particulars — the download needs its size before
// the first byte goes to the browser.
func (t *remoteTree) stream(op HomeOp, withInfo bool) (io.ReadCloser, sandboxfs.FileInfo, error) {
	op.AgentID = t.agentID
	ch, stop, err := t.conn.askStream(context.Background(), TypeHomeOp, op)
	if err != nil {
		return nil, sandboxfs.FileInfo{}, err
	}

	var info sandboxfs.FileInfo
	if withInfo {
		select {
		case msg := <-ch:
			res, err := decode[HomeResult](msg)
			if err != nil {
				stop()
				return nil, sandboxfs.FileInfo{}, err
			}
			if res.Err != "" {
				stop()
				return nil, sandboxfs.FileInfo{}, sandboxfs.ErrorFromKind(res.ErrKind, res.Err)
			}
			if res.Info != nil {
				info = *res.Info
			}
		case <-time.After(homeOpTimeout):
			stop()
			return nil, sandboxfs.FileInfo{}, fmt.Errorf("runner %s does not answer", short(t.conn.runnerID))
		}
	}
	return &chunkReader{ch: ch, stop: stop}, info, nil
}

// chunkReader turns the stream of answers back into a reader.
type chunkReader struct {
	ch   <-chan Message
	stop func()
	rest []byte
	done bool
	err  error
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.rest) == 0 {
		if r.done {
			if r.err != nil {
				return 0, r.err
			}
			return 0, io.EOF
		}
		select {
		case msg, ok := <-r.ch:
			if !ok {
				r.done = true
				// The connection went away mid-download. Reported as an error
				// and not as EOF: a truncated file that arrives as a complete
				// one is worse than a broken download.
				r.err = errors.New("the connection to the runner broke off during the transfer")
				continue
			}
			res, err := decode[HomeResult](msg)
			if err != nil {
				r.done, r.err = true, err
				continue
			}
			r.rest = res.Data
			if res.EOF {
				r.done = true
				if res.Err != "" {
					r.err = sandboxfs.ErrorFromKind(res.ErrKind, res.Err)
				}
			}
		case <-time.After(homeOpTimeout):
			r.done = true
			r.err = errors.New("the runner stopped sending during the transfer")
		}
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

func (r *chunkReader) Close() error {
	r.stop()
	return nil
}
