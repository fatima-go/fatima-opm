package artifact

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxSize int64 = 1024 * 1024 * 1024
const MaxExpanded uint64 = 4 * 1024 * 1024 * 1024

var SafeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Info struct {
	Process   string
	Platforms []string
	BuildJSON string
}

func Digest(filename string) (string, int64, error) {
	f, e := os.Open(filename)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}

func Inspect(filename string) (*Info, error) {
	z, e := zip.OpenReader(filename)
	if e != nil {
		return nil, e
	}
	defer z.Close()
	if len(z.File) > 10000 {
		return nil, fmt.Errorf("too many archive entries")
	}
	info := &Info{}
	rooted := gofarRooted(z.File)
	platforms := map[string]bool{}
	seen := map[string]bool{}
	var expanded uint64
	for _, f := range z.File {
		name := f.Name
		if rooted {
			name = strings.TrimPrefix(name, "/")
		}
		if rooted && name == "" && f.UncompressedSize64 == 0 {
			continue
		}
		n := strings.TrimSuffix(name, "/")
		if n == "" || path.IsAbs(n) || path.Clean(n) != n || n == ".." || strings.HasPrefix(n, "../") || strings.Contains(n, "\\") || (!f.Mode().IsRegular() && !f.Mode().IsDir()) {
			return nil, fmt.Errorf("unsafe archive entry %q", f.Name)
		}
		if seen[n] {
			return nil, fmt.Errorf("duplicate archive entry %q", n)
		}
		seen[n] = true
		if f.UncompressedSize64 > MaxExpanded-expanded {
			return nil, fmt.Errorf("expanded archive exceeds limit")
		}
		expanded += f.UncompressedSize64
		parts := strings.Split(n, "/")
		if len(parts) >= 3 && parts[0] == "platform" {
			platforms[parts[1]] = true
		}
		if n == "deployment.json" {
			if f.UncompressedSize64 > 1024*1024 {
				return nil, fmt.Errorf("deployment metadata too large")
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			b, e := io.ReadAll(io.LimitReader(r, 1024*1024+1))
			r.Close()
			if e != nil {
				return nil, e
			}
			var m struct {
				Process string          `json:"process"`
				Build   json.RawMessage `json:"build"`
			}
			if e = json.Unmarshal(b, &m); e != nil {
				return nil, e
			}
			info.Process = m.Process
			info.BuildJSON = string(m.Build)
		}
	}
	if !SafeName.MatchString(info.Process) {
		return nil, fmt.Errorf("invalid or missing process name")
	}
	for p := range platforms {
		if seen["platform/"+p+"/"+info.Process] {
			info.Platforms = append(info.Platforms, p)
		}
	}
	// Legacy single-platform FARs remain usable, but must be checked by the executor.
	if len(info.Platforms) == 0 && !seen[info.Process] && !seen[info.Process+".sh"] {
		return nil, fmt.Errorf("process executable missing")
	}
	return info, nil
}

// Historical gofar archives use '/' as a virtual archive root, including an
// empty '/' entry and '/deployment.json'. Interpret only that complete layout
// as rooted. Canonical path validation still rejects traversal and collisions;
// extraction never joins an absolute path to the destination.
func gofarRooted(files []*zip.File) bool {
	root, metadata := false, false
	for _, f := range files {
		if !strings.HasPrefix(f.Name, "/") || strings.HasPrefix(f.Name, "//") {
			return false
		}
		if f.Name == "/" && f.UncompressedSize64 == 0 {
			root = true
		}
		if f.Name == "/deployment.json" {
			metadata = true
		}
	}
	return root && metadata
}

// Extract selects a platform without changing the immutable uploaded FAR.
func Extract(filename, dest, platform string) error {
	info, e := Inspect(filename)
	if e != nil {
		return e
	}
	if len(info.Platforms) > 0 {
		found := false
		for _, p := range info.Platforms {
			if p == platform {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unsupported platform %s", platform)
		}
	}
	z, e := zip.OpenReader(filename)
	if e != nil {
		return e
	}
	defer z.Close()
	rooted := gofarRooted(z.File)
	for _, f := range z.File {
		n := f.Name
		if rooted {
			n = strings.TrimPrefix(n, "/")
			if n == "" {
				continue
			}
		}
		if strings.HasPrefix(n, "platform/") {
			prefix := "platform/" + platform + "/"
			if !strings.HasPrefix(n, prefix) {
				continue
			}
			n = strings.TrimPrefix(n, prefix)
			if n == "" {
				continue
			}
		}
		p := filepath.Join(dest, filepath.FromSlash(n))
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(p, 0755); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		mode := f.Mode().Perm() & 0777
		if filepath.Base(p) == info.Process || strings.HasSuffix(p, ".sh") {
			mode |= 0700
		}
		w, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if e != nil {
			r.Close()
			return e
		}
		_, e = io.Copy(w, r)
		r.Close()
		ce := w.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	return nil
}
