// Package cnip reads IP-only source lists. It never widens a conditional rule
// into an unconditional IP match.
package cnip

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

const MaxBytes = 32 << 20
const MaxPrefixes = 200000

func Parse(data []byte, format string) ([]string, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, errors.New("CN IP 文件为空或超过 32 MiB")
	}
	var prefixes []netip.Prefix
	var err error
	switch format {
	case "", "auto":
		if bytes.Contains(data, []byte{0xab, 0xcd, 0xef, 77, 97, 120, 77, 105, 110, 100, 46, 99, 111, 109}) {
			return Parse(data, "mmdb")
		}
		if bytes.HasPrefix(data, []byte("SRS")) {
			return Parse(data, "srs")
		}
		if bytes.IndexByte(data, 0) >= 0 || data[0] == 0x0a && !bytes.Contains(data, []byte(".")) {
			return Parse(data, "dat")
		}
		if result, e := Parse(data, "txt"); e == nil {
			return result, nil
		}
		return Parse(data, "dat")
	case "txt":
		scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
			if line == "" {
				continue
			}
			p, e := netip.ParsePrefix(line)
			if e != nil {
				if a, ae := netip.ParseAddr(line); ae == nil {
					p = netip.PrefixFrom(a, a.BitLen())
				} else {
					return nil, errors.New("TXT 包含无效 IP/CIDR")
				}
			}
			prefixes = append(prefixes, p.Masked())
			if len(prefixes) > MaxPrefixes {
				return nil, errors.New("CN IP 网段过多")
			}
		}
		err = scanner.Err()
	case "mmdb":
		prefixes, err = parseMMDB(data)
	case "dat":
		prefixes, err = parseDAT(data)
	case "srs":
		prefixes, err = parseSRS(data)
	default:
		return nil, errors.New("CN IP 格式必须为 TXT、DAT 或 SRS")
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := []string{}
	for _, p := range prefixes {
		if !p.IsValid() || p.Bits() == 0 || p.Addr().Is4In6() {
			return nil, errors.New("CN IP 包含无效或全网网段")
		}
		v := p.Masked().String()
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	if len(result) == 0 || len(result) > MaxPrefixes {
		return nil, errors.New("CN IP 没有可用网段或条目过多")
	}
	sort.Strings(result)
	return result, nil
}

func fields(data []byte, visit func(protowire.Number, protowire.Type, []byte, uint64) error) error {
	for len(data) > 0 {
		n, t, size := protowire.ConsumeTag(data)
		if size < 0 {
			return errors.New("无效 DAT 字段")
		}
		data = data[size:]
		var b []byte
		var v uint64
		switch t {
		case protowire.BytesType:
			b, size = protowire.ConsumeBytes(data)
		case protowire.VarintType:
			v, size = protowire.ConsumeVarint(data)
		default:
			size = protowire.ConsumeFieldValue(n, t, data)
		}
		if size < 0 {
			return errors.New("DAT 文件截断")
		}
		if err := visit(n, t, b, v); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}
func parseDAT(data []byte) ([]netip.Prefix, error) {
	result := []netip.Prefix{}
	err := fields(data, func(n protowire.Number, t protowire.Type, b []byte, _ uint64) error {
		if n != 1 || t != protowire.BytesType {
			return errors.New("不是 GeoIP DAT 数据库")
		}
		country := ""
		var cidrs [][]byte
		inverted := false
		if err := fields(b, func(n protowire.Number, t protowire.Type, b []byte, v uint64) error {
			switch n {
			case 1:
				country = string(b)
			case 2:
				cidrs = append(cidrs, b)
			case 3:
				inverted = v != 0
			}
			if len(cidrs) > MaxPrefixes {
				return errors.New("DAT 网段过多")
			}
			return nil
		}); err != nil {
			return err
		}
		if !strings.EqualFold(country, "cn") {
			return nil
		}
		if inverted {
			return errors.New("DAT cn 不能使用反向匹配")
		}
		for _, raw := range cidrs {
			var ip []byte
			var bits uint64
			if err := fields(raw, func(n protowire.Number, t protowire.Type, b []byte, v uint64) error {
				if n == 1 {
					ip = b
				}
				if n == 2 {
					bits = v
				}
				return nil
			}); err != nil {
				return err
			}
			addr, ok := netip.AddrFromSlice(ip)
			if !ok || bits > uint64(addr.BitLen()) {
				return errors.New("DAT CIDR 无效")
			}
			result = append(result, netip.PrefixFrom(addr, int(bits)).Masked())
			if len(result) > MaxPrefixes {
				return errors.New("DAT 网段过多")
			}
		}
		return nil
	})
	return result, err
}

// SRS IP sets encode sorted inclusive address ranges. Convert without unsafe
// memory casts and bound both compressed and expanded data before allocating.
func parseSRS(data []byte) ([]netip.Prefix, error) {
	if len(data) < 4 || string(data[:3]) != "SRS" || data[3] < 1 || data[3] > 5 {
		return nil, errors.New("不支持的 SRS 版本")
	}
	z, err := zlib.NewReader(bytes.NewReader(data[4:]))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	raw, err := io.ReadAll(io.LimitReader(z, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return nil, errors.New("SRS 解压失败或过大")
	}
	r := bytes.NewReader(raw)
	count, err := binary.ReadUvarint(r)
	if err != nil || count > MaxPrefixes {
		return nil, errors.New("SRS 规则数量无效")
	}
	result := []netip.Prefix{}
	for i := uint64(0); i < count; i++ {
		typ, e := r.ReadByte()
		if e != nil || typ != 0 {
			return nil, errors.New("SRS 仅支持纯 IP 默认规则")
		}
		seen := false
		for {
			item, e := r.ReadByte()
			if e != nil {
				return nil, e
			}
			if item == 255 {
				invert, e := r.ReadByte()
				if e != nil || invert != 0 || !seen {
					return nil, errors.New("SRS 不支持反向或空规则")
				}
				break
			}
			if item != 6 || seen {
				return nil, errors.New("SRS 包含非 IP 或混合条件")
			}
			seen = true
			version, e := r.ReadByte()
			if e != nil || version != 1 {
				return nil, errors.New("SRS IP 集版本无效")
			}
			var ranges uint64
			if e = binary.Read(r, binary.BigEndian, &ranges); e != nil || ranges > MaxPrefixes {
				return nil, errors.New("SRS 网段数量无效")
			}
			for j := uint64(0); j < ranges; j++ {
				endpoints := make([]netip.Addr, 2)
				for k := range endpoints {
					size, e := binary.ReadUvarint(r)
					if e != nil || size != 4 && size != 16 {
						return nil, errors.New("SRS 地址长度无效")
					}
					b := make([]byte, int(size))
					if _, e = io.ReadFull(r, b); e != nil {
						return nil, e
					}
					endpoints[k], _ = netip.AddrFromSlice(b)
				}
				from, to := endpoints[0], endpoints[1]
				if from.BitLen() != to.BitLen() || from.Compare(to) > 0 {
					return nil, errors.New("SRS 地址范围无效")
				}
				for from.IsValid() && from.Compare(to) <= 0 {
					bits := from.BitLen()
					for bits > 0 {
						p := netip.PrefixFrom(from, bits-1)
						if p.Masked().Addr() != from || lastAddress(p).Compare(to) > 0 {
							break
						}
						bits--
					}
					p := netip.PrefixFrom(from, bits)
					result = append(result, p)
					if len(result) > MaxPrefixes {
						return nil, errors.New("SRS 转换网段过多")
					}
					from = lastAddress(p).Next()
				}
			}
		}
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("SRS 含额外数据")
	}
	return result, nil
}
func lastAddress(p netip.Prefix) netip.Addr {
	b := p.Addr().AsSlice()
	for bit := p.Bits(); bit < len(b)*8; bit++ {
		b[bit/8] |= 1 << uint(7-bit%8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}
