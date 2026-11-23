// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZip_BlocksZipSlip(t *testing.T) {
	maliciousNames := []string{
		"../pwned.txt",
		"subdir/../../pwned.txt",
		"/etc/pwned.txt",
		`..\pwned.txt`,
		`C:\pwned.txt`,
	}

	for _, name := range maliciousNames {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "extract")
			zipPath := filepath.Join(parent, "evil.zip")

			f, err := os.Create(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(f)
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte("EVIL_CONTENT"))
			_ = zw.Close()
			_ = f.Close()

			if err := ExtractZip(zipPath, dest); err == nil {
				t.Fatalf("expected ExtractZip to reject ZipSlip entry %q, but it succeeded", name)
			}

			if _, err := os.Stat(filepath.Join(parent, "pwned.txt")); !os.IsNotExist(err) {
				t.Fatalf("ZipSlip wrote file outside extraction root!")
			}
		})
	}
}

func TestExtractTarGz_BlocksTarSlipAndEscapingSymlinks(t *testing.T) {
	maliciousEntries := []struct {
		name     string
		typeflag byte
		linkname string
	}{
		{name: "../pwned.txt", typeflag: tar.TypeReg},
		{name: "a/b/../../../pwned.txt", typeflag: tar.TypeReg},
		{name: "/etc/pwned.txt", typeflag: tar.TypeReg},
		{name: `..\pwned.txt`, typeflag: tar.TypeReg},
		{name: "escape_link", typeflag: tar.TypeSymlink, linkname: "../../../etc/passwd"},
		{name: "abs_link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	}

	for _, tc := range maliciousEntries {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "extract")
			tarPath := filepath.Join(parent, "evil.tar.gz")

			f, err := os.Create(tarPath)
			if err != nil {
				t.Fatal(err)
			}
			gw := gzip.NewWriter(f)
			tw := tar.NewWriter(gw)

			hdr := &tar.Header{
				Name:     tc.name,
				Mode:     0600,
				Typeflag: tc.typeflag,
				Linkname: tc.linkname,
			}
			if tc.typeflag == tar.TypeReg {
				hdr.Size = int64(len("EVIL_CONTENT"))
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}
			if tc.typeflag == tar.TypeReg {
				_, _ = tw.Write([]byte("EVIL_CONTENT"))
			}
			_ = tw.Close()
			_ = gw.Close()
			_ = f.Close()

			if err := ExtractTarGz(tarPath, dest); err == nil {
				t.Fatalf("expected ExtractTarGz to reject TarSlip entry %q, but it succeeded", tc.name)
			}

			if _, err := os.Stat(filepath.Join(parent, "pwned.txt")); !os.IsNotExist(err) {
				t.Fatalf("TarSlip wrote file outside extraction root!")
			}
		})
	}
}
