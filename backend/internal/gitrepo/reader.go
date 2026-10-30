package gitrepo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Reader reads files of one clone through one long-lived
// `git cat-file --batch`, after ../rongo's: a process per file costs
// milliseconds each, the batch a tenth of one. A path that is not a file at
// that commit (a directory, a submodule pointer) is refused, where
// `git show` would print a listing.
//
// It is for a step that reads its files and ends: the process holds the
// object store open, so a Reader is closed when the step is over and never
// kept. The context given to NewReader is the only bound on a read: ending
// it kills the process, which ends a read blocked on the stream. Reads are
// answered one at a time.
type Reader struct {
	c   *Client
	ctx context.Context
	dir string

	mu     sync.Mutex
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr lockedBuffer
	closed bool
	// broken is set once the stream can no longer be trusted to be in step
	// with the requests: every later read then fails rather than returning
	// another file's bytes under this file's name.
	broken error
}

// ErrReaderBroken marks a read that failed because the batch process is
// gone or its stream is out of step, not because of the file asked for.
var ErrReaderBroken = errors.New("git batch reader is broken")

// TooLargeError is a blob a limited read refused: skipped on the stream,
// never held.
type TooLargeError struct {
	Size int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("a blob of %d bytes is over the read limit", e.Size)
}

// lockedBuffer is git's stderr: written by the goroutine os/exec copies it
// on, read here when a read fails.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// NewReader starts the batch process in the clone at dir. It lives until
// Close, or until ctx ends.
func (c *Client) NewReader(ctx context.Context, dir string) (*Reader, error) {
	r := &Reader{c: c, ctx: ctx, dir: dir}
	r.cmd = c.command(ctx, dir, inRepo(dir), "cat-file", "--batch")
	r.cmd.Stderr = &r.stderr
	in, err := r.cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open reader: %w", err)
	}
	out, err := r.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open reader: %w", err)
	}
	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("open reader: %w", err)
	}
	r.in, r.out = in, bufio.NewReaderSize(out, 64<<10)
	return r, nil
}

// ReadFile reads one path at one commit, refusing a blob over limit bytes
// with a *TooLargeError before holding it. Zero means no limit. A path
// holding a newline is asked of git by argument instead: on the batch's
// input it would end the request early and leave every later answer one
// file out of step.
func (r *Reader) ReadFile(sha, path string, limit int64) ([]byte, error) {
	if err := checkSHA(sha); err != nil {
		return nil, err
	}
	if err := r.ctx.Err(); err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", path, ShortSHA(sha), err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("read %s at %s: the reader is closed", path, ShortSHA(sha))
	}
	if r.broken != nil {
		return nil, fmt.Errorf("read %s at %s: %w", path, ShortSHA(sha), r.broken)
	}
	if strings.Contains(path, "\n") {
		body, err := r.c.readByArgument(r.ctx, r.dir, sha+":"+path, limit)
		if err != nil {
			return nil, fmt.Errorf("read %s at %s: %w", path, ShortSHA(sha), err)
		}
		return body, nil
	}
	body, err := r.read(sha+":"+path, limit)
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", path, ShortSHA(sha), err)
	}
	return body, nil
}

// read sends one request and reads its answer: the header, then for an
// object its bytes and one newline. Whatever is not returned is consumed,
// so the stream stays in step.
func (r *Reader) read(object string, limit int64) ([]byte, error) {
	if _, err := io.WriteString(r.in, object+"\n"); err != nil {
		return nil, r.fail(fmt.Errorf("ask git: %w", err))
	}
	header, err := r.out.ReadString('\n')
	if err != nil {
		return nil, r.fail(fmt.Errorf("read git's answer: %w", err))
	}
	kind, size, err := parseBatchHeader(strings.TrimSuffix(header, "\n"))
	if errors.Is(err, errNoObject) {
		return nil, err
	}
	if err != nil {
		return nil, r.fail(err)
	}
	if kind != "blob" {
		if _, err := io.CopyN(io.Discard, r.out, size+1); err != nil {
			return nil, r.fail(fmt.Errorf("skip a %s: %w", kind, err))
		}
		return nil, fmt.Errorf("not a file at that commit: it is a %s", kind)
	}
	if limit > 0 && size > limit {
		if _, err := io.CopyN(io.Discard, r.out, size+1); err != nil {
			return nil, r.fail(fmt.Errorf("skip %d bytes: %w", size, err))
		}
		return nil, &TooLargeError{Size: size}
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r.out, body); err != nil {
		return nil, r.fail(fmt.Errorf("read %d bytes: %w", size, err))
	}
	if nl, err := r.out.ReadByte(); err != nil || nl != '\n' {
		return nil, r.fail(errors.New("git's answer did not end where its size said"))
	}
	return body, nil
}

// fail marks the stream unusable and reports why; a cancelled step says so
// rather than reading as a repository that cannot be read.
func (r *Reader) fail(err error) error {
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		r.broken = fmt.Errorf("%w: %w", ErrReaderBroken, ctxErr)
		return r.broken
	}
	if msg := strings.TrimSpace(Redact(r.stderr.String())); msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	r.broken = fmt.Errorf("%w: %w", ErrReaderBroken, err)
	return r.broken
}

// Close ends the process. Closing twice is not an error.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	// Closing stdin is how cat-file is told there is nothing more to read.
	_ = r.in.Close()
	// A stream that broke mid-answer may have git blocked writing the rest
	// of an object nobody will read; waiting for it would hang for good.
	if r.broken != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	_ = r.cmd.Wait()
	return nil
}

// readByArgument is the slow way for one object: its type, its size and
// its bytes asked of cat-file with the object in argv, where a newline is
// just a byte. Same refusals as the batch: no object, not a file, over the
// limit.
func (c *Client) readByArgument(ctx context.Context, dir, object string, limit int64) ([]byte, error) {
	kind, err := c.run(ctx, dir, inRepo(dir), "cat-file", "-t", object)
	if err != nil {
		if exitCode(err) == 128 {
			return nil, fmt.Errorf("%w: %w", errNoObject, err)
		}
		return nil, err
	}
	if kind = strings.TrimSpace(kind); kind != "blob" {
		return nil, fmt.Errorf("not a file at that commit: it is a %s", kind)
	}
	sized, err := c.run(ctx, dir, inRepo(dir), "cat-file", "-s", object)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sized), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unparseable size %q", strings.TrimSpace(sized))
	}
	if limit > 0 && size > limit {
		return nil, &TooLargeError{Size: size}
	}
	return c.runIn(ctx, dir, nil, inRepo(dir), "cat-file", "blob", object)
}
