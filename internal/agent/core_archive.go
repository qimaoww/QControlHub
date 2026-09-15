package agent

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/ulikunitz/xz"
)

func extractCoreBinary(engine core.Engine, assetName, archivePath string, output *os.File) error {
	if output == nil {
		return errors.New("core extraction destination is required")
	}
	input, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer input.Close()
	var reader io.Reader
	switch engine {
	case core.EngineMihomo:
		compressed, err := gzip.NewReader(input)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader = compressed
	case core.EngineXray:
		input.Close()
		archive, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer archive.Close()
		for _, entry := range archive.File {
			if (entry.Name == "xray" || entry.Name == "Xray") && entry.Mode().IsRegular() && entry.UncompressedSize64 <= maxReleaseAssetSize {
				entryReader, err := entry.Open()
				if err != nil {
					return err
				}
				defer entryReader.Close()
				reader = entryReader
				break
			}
		}
	case core.EngineSingBox:
		compressed, err := gzip.NewReader(input)
		if err != nil {
			return err
		}
		defer compressed.Close()
		archive := tar.NewReader(compressed)
		for {
			header, err := archive.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == "sing-box" && header.Size <= maxReleaseAssetSize {
				reader = archive
				break
			}
		}
	case core.EngineShadowsocksRust:
		compressed, err := xz.NewReader(input)
		if err != nil {
			return err
		}
		archive := tar.NewReader(compressed)
		for {
			header, err := archive.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == "ssserver" && header.Size <= maxReleaseAssetSize {
				reader = archive
				break
			}
		}
	default:
		return errors.New("unsupported core archive")
	}
	if reader == nil {
		return fmt.Errorf("release asset %s does not contain the expected %s binary", assetName, engine)
	}
	written, err := io.Copy(output, io.LimitReader(reader, maxReleaseAssetSize+1))
	if err != nil {
		return err
	}
	if written < 1<<20 || written > maxReleaseAssetSize {
		return errors.New("extracted core binary size is outside the accepted range")
	}
	return nil
}

func stageXrayReleaseAssets(root *os.Root, assetName, archivePath string) (map[string]string, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	result := make(map[string]string, len(xrayMigrationAssetNames))
	succeeded := false
	defer func() {
		if !succeeded {
			for _, name := range result {
				_ = root.Remove(name)
			}
		}
	}()
	for _, expected := range xrayMigrationAssetNames {
		var entry *zip.File
		for _, candidate := range archive.File {
			if candidate.Name == expected && candidate.Mode().IsRegular() && candidate.UncompressedSize64 <= maxReleaseAssetSize {
				entry = candidate
				break
			}
		}
		if entry == nil {
			return nil, fmt.Errorf("release asset %s does not contain Xray resource %s", assetName, expected)
		}
		tempName, err := randomCoreTempName(root)
		if err != nil {
			return nil, err
		}
		input, err := entry.Open()
		if err != nil {
			return nil, err
		}
		output, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			return nil, err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, maxReleaseAssetSize+1))
		syncErr := error(nil)
		if copyErr == nil {
			syncErr = output.Sync()
		}
		closeErr := output.Close()
		input.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || written < 1<<10 || written > maxReleaseAssetSize {
			_ = root.Remove(tempName)
			if copyErr != nil {
				return nil, copyErr
			}
			if syncErr != nil {
				return nil, syncErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			return nil, fmt.Errorf("Xray resource %s size is outside the accepted range", expected)
		}
		result[expected] = tempName
	}
	succeeded = true
	return result, nil
}
