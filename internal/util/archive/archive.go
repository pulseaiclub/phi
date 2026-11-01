// Package archive extracts release archives with path validation.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extract unpacks archive into dst. Supported formats are "tar.gz" and "zip".
func Extract(ctx context.Context, archive, format, dst string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	switch format {
	case "tar.gz":
		return extractTarGz(archive, dst)
	case "zip":
		return extractZip(archive, dst)
	default:
		return fmt.Errorf("unknown archive format: %s", format)
	}
}

func targetPath(dst, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	root := filepath.Clean(dst)
	target := filepath.Clean(filepath.Join(root, name))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes destination")
	}
	return target, nil
}

func extractTarGz(archivePath, dst string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz %q failed: %w", archivePath, err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("create gzip reader failed: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry failed: %w", err)
		}
		target, err := targetPath(dst, header.Name)
		if err != nil {
			return fmt.Errorf("invalid tar path %q: %w", header.Name, err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create extracted dir %q failed: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create parent dir for %q failed: %w", target, err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("create extracted file %q failed: %w", target, err)
			}
			_, copyErr := io.Copy(out, reader)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("write extracted file %q failed: %w", target, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close extracted file %q failed: %w", target, closeErr)
			}
		}
	}
}

func extractZip(archivePath, dst string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip %q failed: %w", archivePath, err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		target, err := targetPath(dst, entry.Name)
		if err != nil {
			return fmt.Errorf("invalid zip path %q: %w", entry.Name, err)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported zip symlink entry: %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create extracted dir %q failed: %w", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create parent dir for %q failed: %w", target, err)
		}
		src, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q failed: %w", entry.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode())
		if err != nil {
			_ = src.Close()
			return fmt.Errorf("create extracted file %q failed: %w", target, err)
		}
		_, copyErr := io.Copy(out, src)
		srcCloseErr := src.Close()
		outCloseErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("write extracted file %q failed: %w", target, copyErr)
		}
		if srcCloseErr != nil {
			return fmt.Errorf("close zip entry %q failed: %w", entry.Name, srcCloseErr)
		}
		if outCloseErr != nil {
			return fmt.Errorf("close extracted file %q failed: %w", target, outCloseErr)
		}
	}
	return nil
}
