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
	"net"
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
	// Zone is retained only to decode existing accounts; it never restricts new work.
	Zone string `json:"zone,omitempty"`
}
type DNSZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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

type DNSResolver interface {
	LookupCNAME(context.Context, string) (string, error)
	LookupNS(context.Context, string) ([]*net.NS, error)
	LookupTXT(context.Context, string) ([]string, error)
}

// Provider codes are safe identifiers; never retain an API response or a signed
// request URL, either of which can contain account secrets.
type DNSAPIError struct {
	Provider, Operation, Code string
	Status                    int
}

func (e *DNSAPIError) Error() string {
	reason := "操作失败，请核对凭据和区域权限"
	switch {
	case e.Status == 429 || strings.Contains(e.Code, "Limit") || strings.Contains(e.Code, "Throttl"):
		reason = "请求受限，请稍后重试"
	case e.Status == 401 || strings.Contains(e.Code, "AuthFailure") || strings.Contains(e.Code, "InvalidAccessKey") || strings.Contains(e.Code, "Signature") || e.Code == "9109":
		reason = "凭据无效，请更新凭据"
	case e.Status == 403 || strings.Contains(e.Code, "Unauthorized") || strings.Contains(e.Code, "Forbidden") || strings.HasPrefix(e.Code, "OperationDenied."):
		reason = "权限不足，请核对区域读取和 DNS 写入权限"
	case dnsZoneMissing(e):
		reason = "托管区域不存在"
	case e.Status >= 500:
		reason = "服务暂时不可用，请稍后重试"
	}
	return fmt.Sprintf("%s %s：%s（HTTP %d，代码 %s）", e.Provider, e.Operation, reason, e.Status, e.Code)
}
func dnsZoneMissing(err error) bool {
	var apiErr *DNSAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "InvalidDomainName.NoExist", "InvalidDomainName.NotFound", "ResourceNotFound.DomainNotExists", "InvalidParameterValue.DomainNotExists":
		return true
	}
	return false
}
func safeDNSCode(code string) string {
	if code == "" || len(code) > 100 {
		return "unknown"
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_') {
			return "unknown"
		}
	}
	return code
}

func (d DNSClient) Validate() error {
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
		var problem struct {
			Code     string
			Errors   []struct{ Code int }
			Response struct{ Error struct{ Code string } }
		}
		_ = json.Unmarshal(raw, &problem)
		code := problem.Code
		if problem.Response.Error.Code != "" {
			code = problem.Response.Error.Code
		}
		if len(problem.Errors) > 0 {
			code = strconv.Itoa(problem.Errors[0].Code)
		}
		return &DNSAPIError{Provider: d.Provider, Operation: method, Status: resp.StatusCode, Code: safeDNSCode(code)}
	}
	if json.Unmarshal(raw, result) != nil {
		return errors.New("DNS API 响应无效")
	}
	return nil
}
func (d DNSClient) cf(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	raw, _ := json.Marshal(body)
	var response struct {
		Success bool                 `json:"success"`
		Result  json.RawMessage      `json:"result"`
		Errors  []struct{ Code int } `json:"errors"`
	}
	err := d.request(ctx, method, "https://api.cloudflare.com/client/v4"+path, raw, map[string]string{"Authorization": "Bearer " + d.Credentials.Token, "Content-Type": "application/json"}, &response)
	if err == nil && !response.Success {
		code := "unknown"
		if len(response.Errors) > 0 {
			code = strconv.Itoa(response.Errors[0].Code)
		}
		err = &DNSAPIError{Provider: "Cloudflare", Operation: method, Status: 200, Code: code}
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
		var code string
		_ = json.Unmarshal(response["Code"], &code)
		err = &DNSAPIError{Provider: "阿里云 DNS", Operation: action, Status: 200, Code: safeDNSCode(code)}
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
		err = &DNSAPIError{Provider: "DNSPod", Operation: action, Status: 200, Code: safeDNSCode(apiError.Code)}
	}
	return response.Response, err
}
func (d DNSClient) Check(ctx context.Context) error {
	if err := d.Validate(); err != nil {
		return err
	}
	// These reads verify credentials and accessible-list permission only. They do
	// not assert DNS write access to any certificate domain.
	switch d.Provider {
	case "cloudflare":
		_, err := d.cf(ctx, "GET", "/zones?per_page=1", nil)
		return err
	case "alidns":
		_, err := d.ali(ctx, "DescribeDomains", map[string]string{"PageSize": "1"})
		return err
	case "dnspod":
		_, err := d.tencent(ctx, "DescribeDomainList", map[string]any{"Limit": 1})
		return err
	}
	return errors.New("DNS 服务商不支持")
}

// Resolve probes exact names from most specific to least specific. Filtering by
// name works with a token scoped to one zone and never enumerates all zones.
// There is no process/account cache and no mutation of shared credentials.
func (d DNSClient) Resolve(ctx context.Context, name string) (DNSZone, error) {
	if err := d.Validate(); err != nil {
		return DNSZone{}, err
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	for candidate := name; strings.Contains(candidate, "."); candidate = candidate[strings.IndexByte(candidate, '.')+1:] {
		// DNS record labels may contain underscores; provider zone names do not.
		if strings.Contains(candidate, "_") {
			continue
		}
		switch d.Provider {
		case "cloudflare":
			var matched DNSZone
			for page := 1; ; page++ {
				if page > 100 {
					return DNSZone{}, errors.New("Cloudflare 区域分页过多，无法安全选择")
				}
				raw, err := d.cf(ctx, "GET", "/zones?name="+url.QueryEscape(candidate)+"&per_page=50&page="+strconv.Itoa(page), nil)
				if err != nil {
					return DNSZone{}, err
				}
				var zones []struct{ ID, Name string }
				if json.Unmarshal(raw, &zones) != nil {
					return DNSZone{}, errors.New("Cloudflare 区域响应无效")
				}
				for _, z := range zones {
					if z.ID != "" && strings.EqualFold(z.Name, candidate) {
						if matched.ID != "" {
							return DNSZone{}, errors.New("Cloudflare 返回多个同名区域，无法安全选择")
						}
						matched = DNSZone{z.ID, candidate}
					}
				}
				if len(zones) < 50 {
					break
				}
			}
			if matched.ID != "" {
				return matched, nil
			}
		case "alidns":
			raw, err := d.ali(ctx, "DescribeDomainInfo", map[string]string{"DomainName": candidate})
			if err != nil {
				if dnsZoneMissing(err) {
					continue
				}
				return DNSZone{}, err
			}
			var domain string
			if json.Unmarshal(raw["DomainName"], &domain) != nil || !strings.EqualFold(domain, candidate) {
				return DNSZone{}, errors.New("阿里云 DNS 区域响应不匹配")
			}
			return DNSZone{candidate, candidate}, nil
		case "dnspod":
			raw, err := d.tencent(ctx, "DescribeDomain", map[string]any{"Domain": candidate})
			if err != nil {
				if dnsZoneMissing(err) {
					continue
				}
				return DNSZone{}, err
			}
			var info struct{ Domain string }
			if json.Unmarshal(raw["DomainInfo"], &info) != nil || !strings.EqualFold(info.Domain, candidate) {
				return DNSZone{}, errors.New("DNSPod 区域响应不匹配")
			}
			return DNSZone{candidate, candidate}, nil
		}
	}
	return DNSZone{}, fmt.Errorf("未找到 %s 的可访问托管区域，请核对区域及读取权限", name)
}

// Target follows an existing challenge CNAME and rejects a delegated child zone
// that this credential cannot access. A transient resolver failure stops writes.
func (d DNSClient) Target(ctx context.Context, resolver DNSResolver, name string) (string, DNSZone, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	target, err := resolver.LookupCNAME(ctx, name)
	if err != nil {
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
			return "", DNSZone{}, errors.New("DNS 验证目标查询暂时失败，请稍后重试")
		}
		target = name
	}
	target = strings.TrimSuffix(strings.ToLower(target), ".")
	zone, err := d.Resolve(ctx, target)
	if err != nil {
		return "", zone, err
	}
	for child := target; ; child = child[strings.IndexByte(child, '.')+1:] {
		ns, lookupErr := resolver.LookupNS(ctx, child)
		if lookupErr == nil && len(ns) > 0 {
			if child == zone.Name {
				return target, zone, nil
			}
			return "", zone, fmt.Errorf("DNS 验证目标已委派到 %s，当前凭据无法访问该区域", child)
		}
		if lookupErr != nil {
			var dnsErr *net.DNSError
			if !errors.As(lookupErr, &dnsErr) || !dnsErr.IsNotFound {
				return "", zone, fmt.Errorf("DNS 委派查询暂时失败：%s", child)
			}
		}
		if child == zone.Name {
			return "", zone, fmt.Errorf("托管区域 %s 尚无可用的 NS 委派，请检查域名解析设置", zone.Name)
		}
	}
}
func (d DNSClient) Create(ctx context.Context, name, value string) (DNSRecord, error) {
	zone, err := d.Resolve(ctx, name)
	if err != nil {
		return DNSRecord{}, err
	}
	return d.CreateInZone(ctx, zone, name, value)
}
func (d DNSClient) CreateInZone(ctx context.Context, resolved DNSZone, name, value string) (DNSRecord, error) {
	if err := d.Validate(); err != nil {
		return DNSRecord{}, err
	}
	zone := resolved.Name
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if zone == "" || resolved.ID == "" || (name != zone && !strings.HasSuffix(name, "."+zone)) {
		return DNSRecord{}, errors.New("TXT 名称与托管区域不匹配")
	}
	rr := strings.TrimSuffix(name, "."+zone)
	if name == zone {
		rr = "@"
	}
	switch d.Provider {
	case "cloudflare":
		id := resolved.ID
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
		if err == nil && id == "" {
			err = errors.New("DNS API 未返回记录标识")
		}
		return DNSRecord{id, zone}, err
	case "dnspod":
		result, err := d.tencent(ctx, "CreateRecord", map[string]any{"Domain": zone, "SubDomain": rr, "RecordType": "TXT", "RecordLine": "默认", "Value": value, "TTL": 600})
		if err != nil {
			return DNSRecord{}, err
		}
		var id json.Number
		err = json.Unmarshal(result["RecordId"], &id)
		if err == nil && (id == "" || id == "0") {
			err = errors.New("DNS API 未返回记录标识")
		}
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

// Matches verifies a saved receipt without rediscovering its zone. DNSPod's
// record-list index can lag a successful create, so query acknowledged IDs
// directly instead of treating a missing search result as a deleted record.
func (d DNSClient) Matches(ctx context.Context, record DNSRecord, name, value string) (bool, error) {
	if record.ID == "" || record.Zone == "" {
		return false, errors.New("DNS 回执缺少记录或托管区域，已停止核验")
	}
	if d.Provider != "dnspod" {
		records, err := d.FindInZone(ctx, record.Zone, name, value)
		if err != nil {
			return false, err
		}
		for _, existing := range records {
			if existing == record {
				return true, nil
			}
		}
		return false, nil
	}
	id, err := strconv.ParseUint(record.ID, 10, 64)
	if err != nil || id == 0 {
		return false, errors.New("DNSPod 回执记录 ID 无效")
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if name != record.Zone && !strings.HasSuffix(name, "."+record.Zone) {
		return false, errors.New("DNS 验证目标不属于回执托管区域")
	}
	raw, err := d.tencent(ctx, "DescribeRecord", map[string]any{"Domain": record.Zone, "RecordId": id})
	if err != nil {
		var apiErr *DNSAPIError
		if errors.As(err, &apiErr) && apiErr.Code == "InvalidParameter.RecordIdInvalid" {
			return false, nil
		}
		return false, err
	}
	var info struct {
		ID         uint64 `json:"Id"`
		SubDomain  string
		RecordType string
		Value      string
	}
	if json.Unmarshal(raw["RecordInfo"], &info) != nil || info.ID == 0 {
		return false, errors.New("DNSPod 记录响应无效，已保留回执")
	}
	rr := strings.TrimSuffix(name, "."+record.Zone)
	if name == record.Zone {
		rr = "@"
	}
	return info.ID == id && strings.EqualFold(info.SubDomain, rr) && info.RecordType == "TXT" && info.Value == value, nil
}

// Find locates exact values for crash recovery. Callers persist the pre-create
// ID set before a write and hold an order lease for this name/value pair.
func (d DNSClient) Find(ctx context.Context, name, value string) ([]DNSRecord, error) {
	zone, err := d.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return d.FindInZone(ctx, zone.ID, name, value)
}
func (d DNSClient) FindInZone(ctx context.Context, zone, name, value string) ([]DNSRecord, error) {
	if zone == "" {
		return nil, errors.New("DNS 回执缺少托管区域，已停止清理")
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	rr := strings.TrimSuffix(name, "."+zone)
	if name == zone {
		rr = "@"
	}
	out := []DNSRecord{}
	for page := 1; page <= 100; page++ {
		count, limit := 0, 0
		switch d.Provider {
		case "cloudflare":
			id := zone
			raw, err := d.cf(ctx, "GET", "/zones/"+id+"/dns_records?type=TXT&name="+url.QueryEscape(name)+"&per_page=100&page="+strconv.Itoa(page), nil)
			if err != nil {
				return nil, err
			}
			var records []struct{ ID, Name, Content string }
			if err = json.Unmarshal(raw, &records); err != nil {
				return nil, err
			}
			count, limit = len(records), 100
			for _, r := range records {
				if strings.EqualFold(strings.TrimSuffix(r.Name, "."), name) && r.Content == value {
					out = append(out, DNSRecord{r.ID, id})
				}
			}
		case "alidns":
			raw, err := d.ali(ctx, "DescribeDomainRecords", map[string]string{"DomainName": zone, "RRKeyWord": rr, "TypeKeyWord": "TXT", "SearchMode": "EXACT", "PageSize": "500", "PageNumber": strconv.Itoa(page)})
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
			count, limit = len(result.Record), 500
			for _, r := range result.Record {
				if r.RR == rr && r.Type == "TXT" && r.Value == value {
					out = append(out, DNSRecord{r.RecordID, zone})
				}
			}
		case "dnspod":
			raw, err := d.tencent(ctx, "DescribeRecordList", map[string]any{"Domain": zone, "Subdomain": rr, "RecordType": "TXT", "Limit": 3000, "Offset": (page - 1) * 3000})
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
			count, limit = len(records), 3000
			for _, r := range records {
				if r.Name == rr && r.Type == "TXT" && r.Value == value {
					out = append(out, DNSRecord{strconv.FormatUint(r.RecordID, 10), zone})
				}
			}
		default:
			return nil, errors.New("DNS 服务商不支持")
		}
		if count < limit {
			return out, nil
		}
	}
	return nil, errors.New("DNS 记录分页超过安全上限，已停止恢复")
}
