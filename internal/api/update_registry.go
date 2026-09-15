package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type registryManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	} `json:"manifests"`
}

func registryJSON(ctx context.Context, client *http.Client, token, endpoint, accept string, target any) error {
	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if token != "" {
		upstream.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		upstream.Header.Set("Accept", accept)
	}
	upstream.Header.Set("User-Agent", "QControlHub/update-check")
	response, err := client.Do(upstream)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("registry returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 128<<10)).Decode(target); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}

func latestImageVersion(ctx context.Context, client *http.Client) (string, time.Time, error) {
	var authorization struct {
		Token string `json:"token"`
	}
	if err := registryJSON(ctx, client, "", "https://ghcr.io/token?scope=repository:qimaoww/qcontrol-plane:pull&service=ghcr.io", "application/json", &authorization); err != nil {
		return "", time.Time{}, err
	}
	if strings.TrimSpace(authorization.Token) == "" {
		return "", time.Time{}, errors.New("registry authorization token is empty")
	}
	const registryRoot = "https://ghcr.io/v2/qimaoww/qcontrol-plane/"
	const manifestAccept = "application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"
	var manifest registryManifest
	if err := registryJSON(ctx, client, authorization.Token, registryRoot+"manifests/latest", manifestAccept, &manifest); err != nil {
		return "", time.Time{}, err
	}
	if manifest.Config.Digest == "" {
		var selected string
		for _, candidate := range manifest.Manifests {
			if candidate.Platform.OS == "linux" && candidate.Platform.Architecture == "amd64" {
				selected = candidate.Digest
				break
			}
		}
		if selected == "" {
			return "", time.Time{}, errors.New("latest image has no linux/amd64 manifest")
		}
		if err := registryJSON(ctx, client, authorization.Token, registryRoot+"manifests/"+selected, manifestAccept, &manifest); err != nil {
			return "", time.Time{}, err
		}
	}
	if !strings.HasPrefix(manifest.Config.Digest, "sha256:") {
		return "", time.Time{}, errors.New("latest image config digest is invalid")
	}
	var imageConfig struct {
		Created time.Time `json:"created"`
		Config  struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := registryJSON(ctx, client, authorization.Token, registryRoot+"blobs/"+manifest.Config.Digest, "application/vnd.oci.image.config.v1+json", &imageConfig); err != nil {
		return "", time.Time{}, err
	}
	version := strings.TrimSpace(imageConfig.Config.Labels["org.opencontainers.image.version"])
	if !commitVersionPattern.MatchString(strings.ToLower(version)) && !releaseVersionPattern.MatchString(version) {
		return "", time.Time{}, errors.New("latest image version label is invalid")
	}
	return version, imageConfig.Created, nil
}
