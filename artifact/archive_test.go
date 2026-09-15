package artifact

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func far(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.far")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for name, body := range entries {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = w.Write([]byte(body))
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	return path
}
func TestImmutablePlatformSelection(t *testing.T) {
	path := far(t, map[string]string{"deployment.json": `{"process":"example","build":{"author":"test"}}`, "platform/darwin_arm64/example": "mac", "platform/linux_arm64/example": "linux", "application.properties": "key=value"})
	before, _, _ := Digest(path)
	dest := t.TempDir()
	if e := Extract(path, dest, "darwin_arm64"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "example"))
	if string(b) != "mac" {
		t.Fatal("incorrect platform")
	}
	after, _, _ := Digest(path)
	if before != after {
		t.Fatal("original archive changed")
	}
	if e := Extract(path, t.TempDir(), "darwin_amd64"); e == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestHistoricalGofarVirtualRoot(t *testing.T) {
	path := far(t, map[string]string{"/": "", "/deployment.json": `{"process":"example"}`, "/platform/darwin_arm64/example": "mac", "/platform/linux_arm64/example": "linux"})
	before, _, _ := Digest(path)
	dest := t.TempDir()
	if e := Extract(path, dest, "darwin_arm64"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "example"))
	if string(b) != "mac" {
		t.Fatal("gofar platform selection failed")
	}
	after, _, _ := Digest(path)
	if before != after {
		t.Fatal("original FAR was changed")
	}
	bad := far(t, map[string]string{"/": "", "/deployment.json": `{"process":"example"}`, "/example": "exe", "/../escape": "bad"})
	if _, e := Inspect(bad); e == nil {
		t.Fatal("rooted layout escaped canonical validation")
	}
}
func TestUnsafeAndIncompleteArchives(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", "a\\escape"} {
		t.Run(name, func(t *testing.T) {
			path := far(t, map[string]string{"deployment.json": `{"process":"example"}`, "example": "exe", name: "bad"})
			if _, e := Inspect(path); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	if _, e := Inspect(far(t, map[string]string{"deployment.json": `{"process":"example"}`})); e == nil {
		t.Fatal("missing executable accepted")
	}
}
