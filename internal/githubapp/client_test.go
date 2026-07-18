package githubapp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTarGzDropsRootAndRejectsTraversal(t *testing.T) {
	archive := tarball(t, map[string]string{"repo-sha/package.json": "{}", "repo-sha/agents/a.yaml": "name: a"})
	destination := t.TempDir()
	if err := ExtractTarGz(bytes.NewReader(archive), destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "agents", "a.yaml")); err != nil {
		t.Fatal(err)
	}
	bad := tarball(t, map[string]string{"repo-sha/../../escape": "bad"})
	if err := ExtractTarGz(bytes.NewReader(bad), t.TempDir()); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	writer := tar.NewWriter(gzipWriter)
	for name, content := range files {
		data := []byte(content)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
