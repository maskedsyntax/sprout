// Package generator downloads a generated project and places it safely on
// disk. It never deletes or overwrites user files: new projects are staged
// in a temporary directory next to the destination and moved into place
// only after the download and extraction succeeded.
package generator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
)

// Options controls generation.
type Options struct {
	// Dest is the final project directory.
	Dest string
	// Force allows merging into a non-empty existing directory.
	Force bool
	// Limits bounds archive extraction; zero uses DefaultLimits.
	Limits Limits
	Now    func() time.Time
}

// Result summarizes a generation.
type Result struct {
	Dir string `json:"dir"`
	Placement
	Bytes int64 `json:"bytes"`
}

var zipMagic = []byte("PK\x03\x04")

// Endpoint returns the generation endpoint for a project type: the type's
// "action" when advertised, else the HAL link for the type.
func Endpoint(meta *metadata.Metadata, t metadata.Option) string {
	if t.Action != "" {
		return t.Action
	}
	if l, ok := meta.Links[t.ID]; ok && l.Href != "" {
		href := l.Href
		if i := strings.Index(href, "{"); i >= 0 {
			href = href[:i]
		}
		return href
	}
	return "/starter.zip"
}

// Generate downloads the project described by req and places it at opts.Dest.
func Generate(ctx context.Context, client *initializr.Client, meta *metadata.Metadata, req *project.Request, opts Options) (*Result, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	lim := opts.Limits
	if lim == (Limits{}) {
		lim = DefaultLimits
	}
	dest, err := filepath.Abs(opts.Dest)
	if err != nil {
		return nil, fmt.Errorf("resolve output directory: %w", err)
	}
	if _, err := CheckDestination(dest, opts.Force); err != nil {
		return nil, err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", parent, err)
	}
	staging, err := os.MkdirTemp(parent, ".sprout-tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	dl, err := client.Generate(ctx, Endpoint(meta, req.Type), req.Query())
	if err != nil {
		return nil, fmt.Errorf("generate project: %w", err)
	}
	defer dl.Body.Close()

	tree := filepath.Join(staging, "project")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return nil, err
	}
	var size int64
	if req.IsArchive() {
		size, err = downloadArchive(ctx, dl, staging, tree, lim)
	} else {
		size, err = downloadFile(ctx, dl, tree, req, lim)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Re-check: the destination may have changed while downloading.
	if _, err := CheckDestination(dest, opts.Force); err != nil {
		return nil, err
	}
	pl, err := Place(tree, dest, now())
	if err != nil {
		return nil, err
	}
	return &Result{Dir: dest, Placement: *pl, Bytes: size}, nil
}

func downloadArchive(ctx context.Context, dl *initializr.Download, staging, tree string, lim Limits) (int64, error) {
	zipPath := filepath.Join(staging, "project.zip")
	n, err := saveBody(ctx, dl, zipPath, lim.MaxBytes)
	if err != nil {
		return n, err
	}
	head := make([]byte, 4)
	f, err := os.Open(zipPath)
	if err != nil {
		return n, err
	}
	_, rerr := io.ReadFull(f, head)
	f.Close()
	if rerr != nil || !bytes.Equal(head, zipMagic) {
		return n, fmt.Errorf("server returned %q content instead of a ZIP archive", dl.ContentType)
	}
	if _, err := ExtractZip(zipPath, tree, lim); err != nil {
		return n, err
	}
	return n, nil
}

func downloadFile(ctx context.Context, dl *initializr.Download, tree string, req *project.Request, lim Limits) (int64, error) {
	name := dl.Filename
	if name == "" || name == "." || name == "/" {
		name = path.Base(req.Type.Action)
	}
	rel, err := entryPath(name)
	if err != nil || rel == "" || strings.ContainsRune(rel, filepath.Separator) {
		return 0, fmt.Errorf("server suggested an unsafe file name %q", dl.Filename)
	}
	return saveBody(ctx, dl, filepath.Join(tree, rel), lim.MaxBytes)
}

// saveBody streams the response to path, detecting truncated downloads.
func saveBody(ctx context.Context, dl *initializr.Download, p string, maxBytes int64) (int64, error) {
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(dl.Body, maxBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return n, ctxErr
		}
		return n, fmt.Errorf("download interrupted after %d bytes: %w", n, err)
	}
	if n > maxBytes {
		return n, fmt.Errorf("download exceeds %d MB limit", maxBytes>>20)
	}
	if dl.Size > 0 && n != dl.Size {
		return n, fmt.Errorf("download incomplete: received %d of %d bytes", n, dl.Size)
	}
	return n, nil
}

// IsCanceled reports whether err stems from context cancellation.
func IsCanceled(err error) bool { return errors.Is(err, context.Canceled) }
