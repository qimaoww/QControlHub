package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/ulikunitz/xz"
)

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func TestExtractCoreBinaryFormats(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte("binary"), (1<<20)/6+1)
	tests := []struct {
		name   string
		engine core.Engine
		asset  string
		write  func(*testing.T, string)
	}{
		{"Mihomo gzip", core.EngineMihomo, "mihomo.gz", func(t *testing.T, path string) {
			file, _ := os.Create(path)
			writer := gzip.NewWriter(file)
			_, _ = writer.Write(contents)
			_ = writer.Close()
			_ = file.Close()
		}},
		{"Xray zip", core.EngineXray, "Xray-linux-64.zip", func(t *testing.T, path string) {
			file, _ := os.Create(path)
			writer := zip.NewWriter(file)
			entry, _ := writer.Create("xray")
			_, _ = entry.Write(contents)
			_ = writer.Close()
			_ = file.Close()
		}},
		{"sing-box tar gzip", core.EngineSingBox, "sing-box.tar.gz", func(t *testing.T, path string) {
			file, _ := os.Create(path)
			compressed := gzip.NewWriter(file)
			writer := tar.NewWriter(compressed)
			_ = writer.WriteHeader(&tar.Header{Name: "sing-box-test/sing-box", Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg})
			_, _ = writer.Write(contents)
			_ = writer.Close()
			_ = compressed.Close()
			_ = file.Close()
		}},
		{"Shadowsocks Rust tar xz", core.EngineShadowsocksRust, "shadowsocks.tar.xz", func(t *testing.T, path string) {
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			compressed, err := xz.NewWriter(file)
			if err != nil {
				t.Fatal(err)
			}
			writer := tar.NewWriter(compressed)
			if err := writer.WriteHeader(&tar.Header{Name: "shadowsocks-v1.24.0/ssserver", Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(contents); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			archive := filepath.Join(directory, test.asset)
			test.write(t, archive)
			output, err := os.CreateTemp(directory, "output-")
			if err != nil {
				t.Fatal(err)
			}
			if err := extractCoreBinary(test.engine, test.asset, archive, output); err != nil {
				t.Fatal(err)
			}
			_ = output.Close()
			actual, _ := os.ReadFile(output.Name())
			if !bytes.Equal(actual, contents) {
				t.Fatal("extracted binary did not match")
			}
		})
	}
}
