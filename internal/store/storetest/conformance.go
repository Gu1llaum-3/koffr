// Package storetest holds the conformance suite every destination must pass.
//
// E-066 asks for **one** interface implemented by the three destinations of the
// MVP. An interface that each implementation reads differently is not one
// interface, so the promises are written once, here, and every store is held to
// them — the filesystem at lot 2, S3 and SFTP at lot 4.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// New builds a store to test, empty. It is called once per case.
type New func(t *testing.T) backup.Store

// Conformance runs every promise of E-066 against a store.
func Conformance(t *testing.T, newStore New) {
	t.Helper()

	t.Run("writing then reading gives back exactly what was written", func(t *testing.T) {
		store := newStore(t)
		const contents = "an archive, or something shaped like one"

		written, err := store.Write(t.Context(), "shop/2026/09/archive.zst.age", strings.NewReader(contents))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if written != int64(len(contents)) {
			t.Errorf("Write reported %d bytes, want %d", written, len(contents))
		}

		read := readAll(t, store, "shop/2026/09/archive.zst.age")
		if read != contents {
			t.Errorf("read back %q, want %q", read, contents)
		}
	})

	t.Run("listing returns what lies under a prefix, sorted", func(t *testing.T) {
		store := newStore(t)

		for _, path := range []string{
			"shop/2026/09/b.zst.age",
			"shop/2026/09/a.zst.age",
			"shop/2026/08/old.zst.age",
			"erp/2026/09/other.zst.age",
		} {
			if _, err := store.Write(t.Context(), path, strings.NewReader("x")); err != nil {
				t.Fatalf("Write %s: %v", path, err)
			}
		}

		found, err := store.List(t.Context(), "shop/2026/09")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(found) != 2 {
			t.Fatalf("got %d entries, want 2: %+v", len(found), found)
		}
		if found[0].Path >= found[1].Path {
			t.Errorf("the listing is not sorted: %+v", found)
		}
		for _, entry := range found {
			if entry.Bytes != 1 {
				t.Errorf("%s reports %d bytes, want 1", entry.Path, entry.Bytes)
			}
			if entry.At.IsZero() {
				t.Errorf("%s has no date", entry.Path)
			}
		}
	})

	t.Run("listing an empty prefix returns nothing and does not complain", func(t *testing.T) {
		found, err := newStore(t).List(t.Context(), "nothing/here")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(found) != 0 {
			t.Errorf("got %+v, want nothing", found)
		}
	})

	t.Run("deleting removes, and deleting again upsets nobody", func(t *testing.T) {
		store := newStore(t)
		const path = "shop/2026/09/archive.zst.age"

		if _, err := store.Write(t.Context(), path, strings.NewReader("x")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := store.Delete(t.Context(), path); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Read(t.Context(), path); err == nil {
			t.Error("the archive is still readable after being deleted")
		}
		// Retention runs again after a crash: deleting what is gone is fine.
		if err := store.Delete(t.Context(), path); err != nil {
			t.Errorf("deleting twice failed: %v", err)
		}
	})

	t.Run("reading what is not there is an error, not an emptiness", func(t *testing.T) {
		if _, err := newStore(t).Read(t.Context(), "shop/2026/09/never-written"); err == nil {
			t.Error("reading a missing archive succeeded")
		}
	})

	t.Run("checking access", func(t *testing.T) {
		store := newStore(t)

		if err := store.Check(t.Context()); err != nil {
			t.Fatalf("Check refused a destination that works: %v", err)
		}

		// And it left nothing behind (BKP-12).
		found, err := store.List(t.Context(), "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(found) != 0 {
			t.Errorf("Check left %+v behind", found)
		}
	})

	t.Run("an interrupted write leaves nothing visible", func(t *testing.T) {
		store := newStore(t)
		const path = "shop/2026/09/interrupted.zst.age"

		_, err := store.Write(t.Context(), path, io.MultiReader(
			strings.NewReader("the beginning of an archive"),
			failing{},
		))
		if err == nil {
			t.Fatal("an interrupted write reported success")
		}

		// BKP-11 — a path that carries the name of an archive carries a whole
		// archive. Half of one is worse than none: it looks restorable.
		if _, err := store.Read(t.Context(), path); err == nil {
			t.Error("a partial archive is visible under its final name")
		}

		found, err := store.List(t.Context(), "shop")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, entry := range found {
			if entry.Path == path {
				t.Errorf("the interrupted archive is listed: %+v", entry)
			}
		}
	})

	t.Run("writing replaces whatever already had that name", func(t *testing.T) {
		store := newStore(t)
		const path = "shop/2026/09/archive.zst.age"

		for _, contents := range []string{"first", "second and longer"} {
			if _, err := store.Write(t.Context(), path, strings.NewReader(contents)); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}

		if read := readAll(t, store, path); read != "second and longer" {
			t.Errorf("read %q, want the second write", read)
		}
	})
}

func readAll(t *testing.T, store backup.Store, path string) string {
	t.Helper()

	reader, err := store.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read %s: %v", path, err)
	}
	defer func() { _ = reader.Close() }()

	var out bytes.Buffer
	if _, err := io.Copy(&out, reader); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return out.String()
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("the stream broke") }
