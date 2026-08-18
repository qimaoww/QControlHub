package authn

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderAgentID   = "X-QControlHub-Agent-ID"
	HeaderTime      = "X-QControlHub-Timestamp"
	HeaderNonce     = "X-QControlHub-Nonce"
	HeaderSignature = "X-QControlHub-Signature"
	MaxClockSkew    = 90 * time.Second
)

func SignRequest(request *http.Request, body []byte, agentID string, privateKey ed25519.PrivateKey, now time.Time) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid Ed25519 private key")
	}
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	timestamp := strconv.FormatInt(now.UTC().Unix(), 10)
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	signature := ed25519.Sign(privateKey, canonical(request.Method, requestTarget(request.URL), agentID, timestamp, nonce, body))
	request.Header.Set(HeaderAgentID, agentID)
	request.Header.Set(HeaderTime, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(signature))
	return nil
}

func VerifyRequest(request *http.Request, body, publicKey []byte, now time.Time) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", errors.New("invalid registered public key")
	}
	agentID := request.Header.Get(HeaderAgentID)
	if agentID == "" {
		return "", errors.New("missing agent identity")
	}
	if err := ValidateRequestHeaders(request, now); err != nil {
		return "", err
	}
	timestamp := request.Header.Get(HeaderTime)
	nonce := request.Header.Get(HeaderNonce)
	signatureValue := request.Header.Get(HeaderSignature)
	if timestamp == "" || nonce == "" || signatureValue == "" {
		return "", errors.New("missing signed-request headers")
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil || len(nonceBytes) < 16 || len(nonceBytes) > 64 {
		return "", errors.New("invalid request nonce")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureValue)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "", errors.New("invalid request signature encoding")
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), canonical(request.Method, requestTarget(request.URL), agentID, timestamp, nonce, body), signature) {
		return "", errors.New("request signature does not match")
	}
	return nonce, nil
}

func ValidateRequestHeaders(request *http.Request, now time.Time) error {
	timestamp := request.Header.Get(HeaderTime)
	nonce := request.Header.Get(HeaderNonce)
	signature := request.Header.Get(HeaderSignature)
	if timestamp == "" || nonce == "" || signature == "" {
		return errors.New("missing signed-request headers")
	}
	if len(timestamp) > 20 || len(nonce) > 100 || len(signature) > 128 {
		return errors.New("signed-request header is too long")
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid request timestamp")
	}
	requestTime := time.Unix(unix, 0).UTC()
	delta := now.UTC().Sub(requestTime)
	if delta < -MaxClockSkew || delta > MaxClockSkew {
		return fmt.Errorf("request timestamp is outside the %s acceptance window", MaxClockSkew)
	}
	return nil
}

func DecodePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(value) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid encoded Ed25519 private key")
	}
	return ed25519.PrivateKey(value), nil
}

func EncodePrivateKey(key ed25519.PrivateKey) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

func EncodePublicKey(key ed25519.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

func requestTarget(value *url.URL) string {
	path := value.EscapedPath()
	if path == "" {
		path = "/"
	}
	if value.RawQuery != "" {
		path += "?" + value.RawQuery
	}
	return path
}

func canonical(method, target, agentID, timestamp, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	value := strings.Join([]string{
		"qcontrolhub-agent-v1",
		strings.ToUpper(method),
		target,
		agentID,
		timestamp,
		nonce,
		hex.EncodeToString(digest[:]),
	}, "\n")
	return []byte(value)
}
