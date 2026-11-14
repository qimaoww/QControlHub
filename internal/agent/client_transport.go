package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (c *Client) cachedPrivateKey(credential string) (ed25519.PrivateKey, error) {
	c.authKeyMu.Lock()
	if c.authKeyRaw == credential && len(c.authKeyCached) == ed25519.PrivateKeySize {
		cached := c.authKeyCached
		c.authKeyMu.Unlock()
		return cached, nil
	}
	c.authKeyMu.Unlock()

	privateKey, err := authn.DecodePrivateKey(credential)
	if err != nil {
		return nil, err
	}
	c.authKeyMu.Lock()
	c.authKeyRaw = credential
	c.authKeyCached = privateKey
	c.authKeyMu.Unlock()
	return privateKey, nil
}

func (c *Client) doJSON(ctx context.Context, method, path, credential, id string, input, output any) error {
	_, err := c.doJSONStatus(ctx, method, path, credential, id, input, output)
	return err
}

func (c *Client) doJSONStatus(ctx context.Context, method, path, credential, id string, input, output any) (int, error) {
	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body io.Reader
	var encoded []byte
	if input != nil {
		var err error
		encoded, err = json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(requestContext, method, c.config.ServerURL+path, body)
	if err != nil {
		return 0, err
	}
	if id != "" {
		privateKey, err := c.cachedPrivateKey(credential)
		if err != nil {
			return 0, err
		}
		if err := authn.SignRequest(request, encoded, id, privateKey, time.Now().UTC()); err != nil {
			return 0, err
		}
	} else {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, explainTLSError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return response.StatusCode, fmt.Errorf("control plane returned %s: %s", response.Status, strings.TrimSpace(string(contents)))
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(response.Body, core.MaxConfigBytes+256<<10)).Decode(output); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}
