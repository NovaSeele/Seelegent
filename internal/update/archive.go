package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// ErrChecksum is returned when a download does not match its checksum (or the checksum file does not list it).
var ErrChecksum = errors.New("checksum mismatch")

var sumLine = regexp.MustCompile(`^([0-9a-fA-F]{64})\s+\*?(.+)$`)

// expectedSum finds name in a checksum file ("<sha256>  <name>" lines, as goreleaser and sha256sum write them).
func expectedSum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		m := sumLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if len(m) == 3 && strings.TrimSpace(m[2]) == name {
			return strings.ToLower(m[1]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%w: %s does not list %s", ErrChecksum, ChecksumsName, name)
}

// fileSum is the SHA-256 of the file at p, in hex.
func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verify checks the file at p against its line in sums.
func verify(p, name string, sums []byte) error {
	want, err := expectedSum(sums, name)
	if err != nil {
		return err
	}
	got, err := fileSum(p)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%w for %s: expected %s, got %s; nothing was installed", ErrChecksum, name, want, got)
	}
	return nil
}

// binaryName is the executable's file name on goos.
func binaryName(goos string) string {
	if goos == "windows" {
		return ProjectName + ".exe"
	}
	return ProjectName
}

// isBinaryEntry reports whether an archive entry is the executable: at the top or one folder down, nothing else.
func isBinaryEntry(name, bin string) bool {
	name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
	if strings.Contains(name, "..") || path.IsAbs(name) {
		return false
	}
	parts := strings.Split(name, "/")
	return len(parts) <= 2 && parts[len(parts)-1] == bin
}

// extractBinary copies the executable out of the archive at src (zip, else tar.gz) into dst (mode 0755).
func extractBinary(src, dst, goos string) error {
	bin := binaryName(goos)
	if strings.HasSuffix(src, ".zip") {
		return extractZip(src, dst, bin)
	}
	return extractTarGz(src, dst, bin)
}

func extractZip(src, dst, bin string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", path.Base(src), err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isBinaryEntry(f.Name, bin) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeExecutable(dst, rc)
		_ = rc.Close()
		return err
	}
	return fmt.Errorf("%s holds no %s", path.Base(src), bin)
}

func extractTarGz(src, dst, bin string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open %s: %w", path.Base(src), err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s holds no %s", path.Base(src), bin)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", path.Base(src), err)
		}
		if h.Typeflag == tar.TypeReg && isBinaryEntry(h.Name, bin) {
			return writeExecutable(dst, tr)
		}
	}
}

// writeExecutable writes at most maxArchive bytes of r into the new file dst, mode 0755, synced.
func writeExecutable(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755) //nolint:gosec // an executable
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxArchive+1))
	if err == nil && n > maxArchive {
		err = fmt.Errorf("%s is larger than %d bytes", path.Base(dst), maxArchive)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// copyExecutable copies src into the new file dst (mode 0755, synced).
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeExecutable(dst, in)
}
