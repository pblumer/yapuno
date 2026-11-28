package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/webpush"
)

func TestCreatesAPrivateKeyOnceAndPrintsOnlyThePublicKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "push-key.pem")
	var out bytes.Buffer
	if err := run([]string{path}, &out); err != nil {
		t.Fatal(err)
	}
	key, err := webpush.LoadKey(path, "https://app.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != key.PublicKey() || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("output: %q", out.String())
	}
	before, _ := os.ReadFile(path)
	if err := run([]string{path}, &out); err == nil {
		t.Fatal("existing key overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("key changed")
	}
}

func TestRefusesAMissingPathOrAnOpenDirectory(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("ran without a path")
	}
	if err := run([]string{"a", "b"}, &bytes.Buffer{}); err == nil {
		t.Fatal("ran with two paths")
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{filepath.Join(open, "k.pem")}, &bytes.Buffer{}); err == nil {
		t.Fatal("key written into a directory others can read")
	}
}
