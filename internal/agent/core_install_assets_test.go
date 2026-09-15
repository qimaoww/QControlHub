package agent

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStageXrayReleaseAssetsRequiresAndExtractsGeoData(t *testing.T) {
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "Xray-linux-64.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range xrayMigrationAssetNames {
		entry, entryErr := writer.Create(name)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		if _, entryErr = entry.Write(bytes.Repeat([]byte(name), 1024)); entryErr != nil {
			t.Fatal(entryErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	assets, err := stageXrayReleaseAssets(root, filepath.Base(archivePath), archivePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range xrayMigrationAssetNames {
		tempName := assets[name]
		contents, readErr := os.ReadFile(filepath.Join(directory, tempName))
		if readErr != nil || !bytes.Contains(contents, []byte(name)) {
			t.Fatalf("staged Xray resource %s = %q, %v", name, contents, readErr)
		}
	}
}
