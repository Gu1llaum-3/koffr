// P-002: reconstruct a PostgreSQL backup_manifest by walking a base backup tar.
//
// Reads an uncompressed tar on stdin, emits one JSON object per regular file
// with the fields backup_manifest records. Throwaway probe code.
package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type entry struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
	Modified string `json:"modified"`
	Format   string `json:"format"` // which tar header flavour carried this entry
	Type     byte   `json:"type"`
}

func main() {
	tr := tar.NewReader(os.Stdin)
	enc := json.NewEncoder(os.Stdout)

	var files, dirs, links, other int
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "tar error:", err)
			os.Exit(1)
		}

		switch h.Typeflag {
		case tar.TypeDir:
			dirs++
			continue
		case tar.TypeSymlink, tar.TypeLink:
			links++
			// Tablespace symlinks matter for a restore, so record them.
			if err := enc.Encode(entry{Path: h.Name, Size: 0, Checksum: "", Format: h.Format.String(), Type: h.Typeflag}); err != nil {
				fmt.Fprintln(os.Stderr, "encode:", err)
				os.Exit(1)
			}
			continue
		case tar.TypeReg:
			files++
		default:
			other++
			continue
		}

		sum := sha256.New()
		n, err := io.Copy(sum, tr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read", h.Name, err)
			os.Exit(1)
		}
		if err := enc.Encode(entry{
			Path:     h.Name,
			Size:     n,
			Checksum: hex.EncodeToString(sum.Sum(nil)),
			Modified: h.ModTime.UTC().Format("2006-01-02 15:04:05") + " GMT",
			Format:   h.Format.String(),
			Type:     h.Typeflag,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
	}

	fmt.Fprintf(os.Stderr, "walked: %d files, %d dirs, %d links, %d other\n", files, dirs, links, other)
}
