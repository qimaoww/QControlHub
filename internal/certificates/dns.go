package certificates

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DNSCredentials struct {
	Token           string `json:"token,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	AccessKeySecret string `json:"access_key_secret,omitempty"`
	SecretID        string `json:"secret_id,omitempty"`
	SecretKey       string `json:"secret_key,omitempty"`
	Zone            string `json:"zone"`
}
type DNSRecord struct {
	ID   string `json:"id"`
	Zone string `json:"zone"`
}
type DNSClient struct {
	Provider    string
	Credentials DNSCredentials
	HTTP        *http.Client
}

func (d DNSClient) Validate() error {
	names, err := Domains([]string{d.Credentials.Zone})
	if err != nil || len(names) != 1 || strings.HasPrefix(d.Credentials.Zone, "*.") {
		return errors.New("请填写准确的 DNS 托管主域名")
	}
	switch d.Provider {
	case "cloudflare":
		if d.Credentials.Token != "" {
			return nil
		}
	case "alidns":
		if d.Credentials.AccessKeyID != "" && d.Credentials.AccessKeySecret != "" {
			return nil
		}
	case "dnspod":
		if d.Credentials.SecretID != "" && d.Credentials.SecretKey != "" {
			return nil
		}
	default:
		return errors.New("DNS 服务商不支持")
	}
	return errors.New("DNS API 凭据不完整")
}
func (d DNSClient) request(ctx context.Context, method, endpoint string, body []byte, headers map[string]string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := d.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("DNS API 连接失败，请检查网络与凭据权限")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return errors.New("DNS API 响应读取失败")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DNS API 返回 HTTP %d，请检查凭据与区域权限", resp.StatusCode)
	}
	if json.Unmarshal(raw, result) != nil {
		return errors.New("DNS API 响应无效")
	}
	return nil
}
func (d DNSClient) cf(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	raw, _ := json.Marshal(body)
	var response struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	err := d.request(ctx, method, "https://api.cloudflare.com/client/v4"+path, raw, map[string]string{"Authorization": "Bearer " + d.Credentials.Token, "Content-Type": "application/json"}, &response)
	if err == nil && !response.Success {
		err = errors.New("Cloudflare 拒绝操作，请检查 Zone:Read 与 DNS:Edit 权限")
	}
	return response.Result, err
}
func hmacBytes(newHash string, key []byte, data string) []byte {
	if newHash == "sha1" {
		h := hmac.New(sha1.New, key)
		h.Write([]byte(data))
		return h.Sum(nil)
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
func hashHex(value []byte) string   { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func aliEscape(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }
func (d DNSClient) ali(ctx context.Context, action string, params map[string]string) (map[string]json.RawMessage, error) {
	params["Action"] = action
	params["Version"] = "2015-01-09"
	params["Format"] = "JSON"
	params["AccessKeyId"] = d.Credentials.AccessKeyID
	params["SignatureMethod"] = "HMAC-SHA1"
	params["SignatureVersion"] = "1.0"
	params["Timestamp"] = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	params["SignatureNonce"] = strconv.FormatInt(time.Now().UnixNano(), 10)
	keys := []string{}
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, aliEscape(k)+"="+aliEscape(params[k]))
	}
	query := strings.Join(parts, "&")
	sig := base64.StdEncoding.EncodeToString(hmacBytes("sha1", []byte(d.Credentials.AccessKeySecret+"&"), "GET&%2F&"+aliEscape(query)))
	var response map[string]json.RawMessage
	err := d.request(ctx, "GET", "https://alidns.aliyuncs.com/?"+query+"&Signature="+aliEscape(sig), nil, nil, &response)
	if err == nil && response["Code"] != nil {
		err = errors.New("阿里云 DNS 拒绝操作，请检查域名与 AddDomainRecord/DeleteDomainRecord/DescribeDomainRecords 权限")
	}
	return response, err
}
func (d DNSClient) tencent(ctx context.Context, action string, params map[string]any) (map[string]json.RawMessage, error) {
	payload, _ := json.Marshal(params)
	now := time.Now().UTC()
	stamp := strconv.FormatInt(now.Unix(), 10)
	date := now.Format("2006-01-02")
	canonical := "POST\n/\n\ncontent-type:application/json; charset=utf-8\nhost:dnspod.tencentcloudapi.com\n\ncontent-type;host\n" + hashHex(payload)
	scope := date + "/dnspod/tc3_request"
	toSign := "TC3-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hashHex([]byte(canonical))
	key := hmacBytes("sha256", []byte("TC3"+d.Credentials.SecretKey), date)
	key = hmacBytes("sha256", key, "dnspod")
	key = hmacBytes("sha256", key, "tc3_request")
	signature := hex.EncodeToString(hmacBytes("sha256", key, toSign))
	headers := map[string]string{"Content-Type": "application/json; charset=utf-8", "X-TC-Action": action, "X-TC-Version": "2021-03-23", "X-TC-Timestamp": stamp, "Authorization": "TC3-HMAC-SHA256 Credential=" + d.Credentials.SecretID + "/" + scope + ", SignedHeaders=content-type;host, Signature=" + signature}
	var response struct{ Response map[string]json.RawMessage }
	err := d.request(ctx, "POST", "https://dnspod.tencentcloudapi.com/", payload, headers, &response)
	if err == nil && response.Response["Error"] != nil {
		var apiError struct{ Code string }
		_ = json.Unmarshal(response.Response["Error"], &apiError)
		if action == "DescribeRecordList" && apiError.Code == "ResourceNotFound.NoDataOfRecord" {
			return map[string]json.RawMessage{"RecordList": json.RawMessage(`[]`)}, nil
		}
		err = errors.New("腾讯云 DNSPod 拒绝操作，请检查域名及 CreateRecord/DeleteRecord/DescribeRecordList 权限")
	}
	return response.Response, err
}
func (d DNSClient) Check(ctx context.Context) error {
	if err := d.Validate(); err != nil {
		return err
	}
	switch d.Provider {
	case "cloudflare":
		_, err := d.zoneID(ctx)
		return err
	case "alidns":
		_, err := d.ali(ctx, "DescribeDomainRecords", map[string]string{"DomainName": d.Credentials.Zone, "PageSize": "1"})
		return err
	case "dnspod":
		_, err := d.tencent(ctx, "DescribeDomain", map[string]any{"Domain": d.Credentials.Zone})
		return err
	}
	return errors.New("服务商不支持")
}
func (d DNSClient) zoneID(ctx context.Context) (string, error) {
	raw, err := d.cf(ctx, "GET", "/zones?name="+url.QueryEscape(d.Credentials.Zone), nil)
	if err != nil {
		return "", err
	}
	var zones []struct{ ID string }
	if json.Unmarshal(raw, &zones) != nil || len(zones) != 1 || zones[0].ID == "" {
		return "", errors.New("Cloudflare 托管区域未找到")
	}
	return zones[0].ID, nil
}
func (d DNSClient) Create(ctx context.Context, name, value string) (DNSRecord, error) {
	if err := d.Validate(); err != nil {
		return DNSRecord{}, err
	}
	zone := strings.TrimSuffix(strings.ToLower(d.Credentials.Zone), ".")
	if !strings.HasSuffix(name, "."+zone) {
		return DNSRecord{}, errors.New("TXT 名称不属于此 DNS 账号的托管区域")
	}
	rr := strings.TrimSuffix(name, "."+zone)
	switch d.Provider {
	case "cloudflare":
		id, err := d.zoneID(ctx)
		if err != nil {
			return DNSRecord{}, err
		}
		raw, err := d.cf(ctx, "POST", "/zones/"+id+"/dns_records", map[string]any{"type": "TXT", "name": name, "content": value, "ttl": 120})
		if err != nil {
			return DNSRecord{}, err
		}
		var record struct{ ID string }
		err = json.Unmarshal(raw, &record)
		if record.ID == "" {
			return DNSRecord{}, errors.New("DNS API 未返回记录标识")
		}
		return DNSRecord{record.ID, id}, err
	case "alidns":
		result, err := d.ali(ctx, "AddDomainRecord", map[string]string{"DomainName": zone, "RR": rr, "Type": "TXT", "Value": value, "TTL": "600"})
		if err != nil {
			return DNSRecord{}, err
		}
		var id string
		err = json.Unmarshal(result["RecordId"], &id)
		return DNSRecord{id, zone}, err
	case "dnspod":
		result, err := d.tencent(ctx, "CreateRecord", map[string]any{"Domain": zone, "SubDomain": rr, "RecordType": "TXT", "RecordLine": "默认", "Value": value, "TTL": 600})
		if err != nil {
			return DNSRecord{}, err
		}
		var id json.Number
		err = json.Unmarshal(result["RecordId"], &id)
		return DNSRecord{string(id), zone}, err
	}
	return DNSRecord{}, errors.New("DNS 服务商不支持")
}
func (d DNSClient) Delete(ctx context.Context, record DNSRecord) error {
	if record.ID == "" {
		return nil
	}
	switch d.Provider {
	case "cloudflare":
		_, err := d.cf(ctx, "DELETE", "/zones/"+url.PathEscape(record.Zone)+"/dns_records/"+url.PathEscape(record.ID), nil)
		return err
	case "alidns":
		_, err := d.ali(ctx, "DeleteDomainRecord", map[string]string{"RecordId": record.ID})
		return err
	case "dnspod":
		id, err := strconv.ParseUint(record.ID, 10, 64)
		if err != nil {
			return err
		}
		_, err = d.tencent(ctx, "DeleteRecord", map[string]any{"Domain": record.Zone, "RecordId": id})
		return err
	}
	return errors.New("DNS 服务商不支持")
}

// Find locates exact values for crash recovery. Callers persist the pre-create
// ID set before a write and hold an order lease for this name/value pair.
func (d DNSClient) Find(ctx context.Context, name, value string) ([]DNSRecord, error) {
	zone := strings.TrimSuffix(strings.ToLower(d.Credentials.Zone), ".")
	if !strings.HasSuffix(name, "."+zone) {
		return nil, errors.New("TXT 名称不属于托管区域")
	}
	rr := strings.TrimSuffix(name, "."+zone)
	out := []DNSRecord{}
	switch d.Provider {
	case "cloudflare":
		id, err := d.zoneID(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := d.cf(ctx, "GET", "/zones/"+id+"/dns_records?type=TXT&name="+url.QueryEscape(name)+"&per_page=100", nil)
		if err != nil {
			return nil, err
		}
		var records []struct{ ID, Name, Content string }
		if err = json.Unmarshal(raw, &records); err != nil {
			return nil, err
		}
		if len(records) >= 100 {
			return nil, errors.New("同名 DNS 记录过多，无法安全恢复")
		}
		for _, r := range records {
			if strings.EqualFold(strings.TrimSuffix(r.Name, "."), name) && r.Content == value {
				out = append(out, DNSRecord{r.ID, id})
			}
		}
	case "alidns":
		raw, err := d.ali(ctx, "DescribeDomainRecords", map[string]string{"DomainName": zone, "RRKeyWord": rr, "TypeKeyWord": "TXT", "SearchMode": "EXACT", "PageSize": "500"})
		if err != nil {
			return nil, err
		}
		var result struct {
			Record []struct {
				RecordID        string `json:"RecordId"`
				RR, Type, Value string
			}
		}
		if err = json.Unmarshal(raw["DomainRecords"], &result); err != nil {
			return nil, err
		}
		if len(result.Record) >= 500 {
			return nil, errors.New("同名 DNS 记录过多，无法安全恢复")
		}
		for _, r := range result.Record {
			if r.RR == rr && r.Type == "TXT" && r.Value == value {
				out = append(out, DNSRecord{r.RecordID, zone})
			}
		}
	case "dnspod":
		raw, err := d.tencent(ctx, "DescribeRecordList", map[string]any{"Domain": zone, "Subdomain": rr, "RecordType": "TXT", "Limit": 3000})
		if err != nil {
			return nil, err
		}
		var records []struct {
			RecordID          uint64 `json:"RecordId"`
			Name, Type, Value string
		}
		if err = json.Unmarshal(raw["RecordList"], &records); err != nil {
			return nil, err
		}
		if len(records) >= 3000 {
			return nil, errors.New("同名 DNS 记录过多，无法安全恢复")
		}
		for _, r := range records {
			if r.Name == rr && r.Type == "TXT" && r.Value == value {
				out = append(out, DNSRecord{strconv.FormatUint(r.RecordID, 10), zone})
			}
		}
	default:
		return nil, errors.New("DNS 服务商不支持")
	}
	return out, nil
}
