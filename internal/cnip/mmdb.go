package cnip

import (
	"encoding/binary"
	"errors"
	"github.com/oschwald/maxminddb-golang"
	"net/netip"
	"strings"
)

func parseMMDB(data []byte) ([]netip.Prefix, error) {
	db, err := maxminddb.FromBytes(data)
	if err != nil {
		return nil, errors.New("无效 MMDB 数据库")
	}
	defer db.Close()
	// chnroutes2 has unused dictionary data and no description metadata.
	// Decode the referenced networks instead of requiring canonical metadata.
	if err := validateMMDBTree(data, db.Metadata.NodeCount, db.Metadata.RecordSize); err != nil {
		return nil, err
	}
	networks := db.Networks(maxminddb.SkipAliasedNetworks)
	result := []netip.Prefix{}
	count := 0
	for networks.Next() {
		count++
		if count > 2000000 {
			return nil, errors.New("MMDB 网络条目过多")
		}
		var record struct {
			Country struct {
				ISOCode string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
			CountryCode string `maxminddb:"country_code"`
		}
		network, err := networks.Network(&record)
		if err != nil {
			return nil, err
		}
		code := record.Country.ISOCode
		if code == "" {
			code = record.CountryCode
		}
		if !strings.EqualFold(code, "CN") {
			continue
		}
		p, err := netip.ParsePrefix(network.String())
		if err != nil {
			return nil, err
		}
		result = append(result, p)
		if len(result) > MaxPrefixes {
			return nil, errors.New("MMDB CN 网段过多")
		}
	}
	return result, networks.Err()
}

// Validate search-node references before traversal. A malformed downloadable
// database must not trap the iterator in a cycle or an excessive-depth path.
func validateMMDBTree(data []byte, count uint, size uint) error {
	width := int(size / 4)
	if count == 0 || count > 4000000 || (size != 24 && size != 28 && size != 32) || uint64(count)*uint64(width) > uint64(len(data)) {
		return errors.New("MMDB 搜索树大小无效")
	}
	states := make([]byte, int(count))
	type frame struct {
		node  uint32
		depth int
		exit  bool
	}
	stack := []frame{{0, 0, false}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if uint(f.node) >= count {
			continue
		}
		if f.exit {
			states[f.node] = 2
			continue
		}
		if f.depth > 128 || states[f.node] == 1 {
			return errors.New("MMDB 搜索树循环或过深")
		}
		if states[f.node] == 2 {
			continue
		}
		states[f.node] = 1
		b := data[int(f.node)*width : int(f.node)*width+width]
		var left, right uint32
		switch size {
		case 24:
			left = uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
			right = uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5])
		case 28:
			left = uint32(b[3]&0xf0)<<20 | uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
			right = uint32(b[3]&15)<<24 | uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6])
		case 32:
			left = binary.BigEndian.Uint32(b[:4])
			right = binary.BigEndian.Uint32(b[4:])
		}
		stack = append(stack, frame{f.node, f.depth, true}, frame{right, f.depth + 1, false}, frame{left, f.depth + 1, false})
	}
	return nil
}
