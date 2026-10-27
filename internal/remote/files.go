package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"

	"github.com/erfuun/managify/internal/appdir"
	"github.com/erfuun/managify/internal/sshx"
)

// Progress reports bytes done out of total.
type Progress func(done, total int64)

const progressStep = 64 * 1024

// ResolveRemotePath turns what the user typed into a full remote path:
// "~" and "~/x" are the home dir, and a path ending in "/" (or empty, or
// "~") means "into that directory, with the local file name".
func ResolveRemotePath(remote, localBase, home string) string {
	r := strings.TrimSpace(remote)
	into := r == "" || r == "~" || strings.HasSuffix(r, "/")
	switch {
	case r == "" || r == "~":
		r = home
	case strings.HasPrefix(r, "~/"):
		r = path.Join(home, r[2:])
	}
	if into {
		return path.Join(r, localBase)
	}
	return path.Clean(r)
}

// Upload copies localPath to the server, overwriting, and returns the final
// remote path.
func Upload(ctx context.Context, c sshx.Client, localPath, remotePath string, p Progress) (string, error) {
	sc, stop, err := openSFTP(ctx, c)
	if err != nil {
		return "", err
	}
	defer stop()
	home, err := sc.Getwd()
	if err != nil {
		return "", err
	}
	final := ResolveRemotePath(remotePath, filepath.Base(localPath), home)
	// Like scp, an existing directory typed without "/" means "into it".
	if st, err := sc.Stat(final); err == nil && st.IsDir() {
		final = path.Join(final, filepath.Base(localPath))
	}

	src, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return "", err
	}
	dst, err := sc.OpenFile(final, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return "", ctxErr(ctx, fmt.Errorf("open %s on the server: %w", final, err))
	}
	_, err = io.Copy(dst, &progressReader{r: src, total: st.Size(), p: p})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", ctxErr(ctx, err)
	}
	report(p, st.Size(), st.Size())
	return final, nil
}

// Download copies one remote file into localDir (created 0700) as a 0600
// file and returns its local path. The file appears only once complete.
func Download(ctx context.Context, c sshx.Client, remotePath, localDir string, p Progress) (string, error) {
	sc, stop, err := openSFTP(ctx, c)
	if err != nil {
		return "", err
	}
	defer stop()
	home, err := sc.Getwd()
	if err != nil {
		return "", err
	}
	final := ResolveRemotePath(remotePath, "", home)
	src, err := sc.Open(final)
	if err != nil {
		return "", ctxErr(ctx, err)
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return "", ctxErr(ctx, err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory; only single files can be downloaded", final)
	}
	if err := appdir.EnsureDir(localDir); err != nil {
		return "", err
	}
	// Remote names may hold characters Windows forbids, like ":".
	local := filepath.Join(localDir, appdir.SafeName(path.Base(final), "download"))
	part := local + ".part"
	dst, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(dst, &progressReader{r: src, total: st.Size(), p: p})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, local)
	}
	if err != nil {
		os.Remove(part)
		return "", ctxErr(ctx, err)
	}
	report(p, st.Size(), st.Size())
	return local, nil
}

// HasCRLF reports Windows line endings in a text file: "\r\n" in the first
// MiB, and no NUL in the first 8 KiB (which would make it binary).
func HasCRLF(localPath string) (bool, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, err
	}
	buf = buf[:n]
	if bytes.IndexByte(buf[:min(n, 8<<10)], 0) >= 0 {
		return false, nil
	}
	return bytes.Contains(buf, []byte("\r\n")), nil
}

// openSFTP starts an SFTP session that ctx cancellation closes.
func openSFTP(ctx context.Context, c sshx.Client) (*sftp.Client, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	sc, err := c.NewSFTP()
	if err != nil {
		return nil, nil, ctxErr(ctx, err)
	}
	stopAfter := context.AfterFunc(ctx, func() { sc.Close() })
	return sc, func() { stopAfter(); sc.Close() }, nil
}

// ctxErr prefers the cancellation over the error it caused.
func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type progressReader struct {
	r          io.Reader
	done, last int64
	total      int64
	p          Progress
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	pr.done += int64(n)
	if pr.done-pr.last >= progressStep && pr.done < pr.total {
		pr.last = pr.done
		report(pr.p, pr.done, pr.total)
	}
	return n, err
}

func report(p Progress, done, total int64) {
	if p != nil {
		p(done, total)
	}
}
