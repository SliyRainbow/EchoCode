package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalResolvesExistingAncestorForMissingPath(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	target, err := Canonical(filepath.Join(root, "missing", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "missing", "file.txt")
	if target != want {
		t.Fatalf("canonical target = %q, want %q", target, want)
	}
}

func TestWithinTreatsCanonicalAndOriginalTempPathsAsEquivalent(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := Within(root, filepath.Join(canonicalRoot, "child", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Fatalf("%q should contain its canonical form %q", root, canonicalRoot)
	}
}

func TestWithinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	inside, err := Within(root, filepath.Join(link, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if inside {
		t.Fatal("path through an escaping symlink should be outside the root")
	}
}
