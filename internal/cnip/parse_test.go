package cnip

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"google.golang.org/protobuf/encoding/protowire"
	"os"
	"reflect"
	"testing"
)

func TestTXT(t *testing.T) {
	got, err := Parse([]byte("# CN\n1.0.1.1/24\n1.0.1.0/24\n2001:db8::1\n"), "txt")
	if err != nil || !reflect.DeepEqual(got, []string{"1.0.1.0/24", "2001:db8::1/128"}) {
		t.Fatalf("%v %v", got, err)
	}
	for _, v := range []string{"", "0.0.0.0/0", "DOMAIN,example.com", "1.2.3.999", "<html>error</html>"} {
		if _, err := Parse([]byte(v), "txt"); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
}
func protoBytes(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func TestDAT(t *testing.T) {
	cidr := append(protoBytes(1, []byte{1, 0, 1, 0}), protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 24)...)
	country := append(protoBytes(1, []byte("CN")), protoBytes(2, cidr)...)
	data := protoBytes(1, country)
	for _, format := range []string{"dat", "auto"} {
		got, err := Parse(data, format)
		if err != nil || len(got) != 1 || got[0] != "1.0.1.0/24" {
			t.Fatalf("%v %v", got, err)
		}
	}
	if _, err := Parse(protoBytes(1, append(protoBytes(1, []byte("US")), protoBytes(2, cidr)...)), "dat"); err == nil {
		t.Fatal("accepted missing CN")
	}
	if _, err := Parse(data[:len(data)-1], "dat"); err == nil {
		t.Fatal("accepted truncated DAT")
	}
}
func TestDATSkipsLargeUnrelatedCountry(t *testing.T) {
	cidr := append(protoBytes(1, []byte{1, 0, 1, 0}), protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 24)...)
	unrelated := bytes.Repeat(protoBytes(2, cidr), MaxPrefixes+1)
	unrelated = append(unrelated, protoBytes(1, []byte("US"))...)
	cn := append(protoBytes(2, cidr), protoBytes(1, []byte("CN"))...)
	data := append(protoBytes(1, unrelated), protoBytes(1, cn)...)
	got, err := Parse(data, "auto")
	if err != nil || !reflect.DeepEqual(got, []string{"1.0.1.0/24"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func makeSRS(item byte, invert byte) []byte {
	var raw bytes.Buffer
	raw.WriteByte(1)
	raw.WriteByte(0)
	raw.WriteByte(item)
	raw.WriteByte(1)
	binary.Write(&raw, binary.BigEndian, uint64(1))
	raw.WriteByte(4)
	raw.Write([]byte{1, 0, 1, 0})
	raw.WriteByte(4)
	raw.Write([]byte{1, 0, 1, 255})
	raw.WriteByte(255)
	raw.WriteByte(invert)
	var out bytes.Buffer
	out.Write([]byte{'S', 'R', 'S', 3})
	z := zlib.NewWriter(&out)
	z.Write(raw.Bytes())
	z.Close()
	return out.Bytes()
}
func TestSRS(t *testing.T) {
	got, err := Parse(makeSRS(6, 0), "srs")
	if err != nil || len(got) != 1 || got[0] != "1.0.1.0/24" {
		t.Fatalf("%v %v", got, err)
	}
	for _, data := range [][]byte{makeSRS(5, 0), makeSRS(6, 1), []byte("SRS\xff")} {
		if _, err := Parse(data, "srs"); err == nil {
			t.Fatal("accepted unsupported condition")
		}
	}
}
func TestRealSource(t *testing.T) {
	file := os.Getenv("QCH_CNIP_TEST_FILE")
	if file == "" {
		t.Skip("optional real upstream sample")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 1000 {
		t.Fatalf("too few CN networks: %d", len(got))
	}
	t.Logf("parsed %d CN prefixes", len(got))
}

func TestChnroutesMMDB(t *testing.T) {
	data, err := os.ReadFile("testdata/chnroutes.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"auto", "mmdb"} {
		got, err := Parse(data, format)
		if err != nil || len(got) != 3895 {
			t.Fatalf("%s: %d %v", format, len(got), err)
		}
	}
	if _, err := Parse(data[:len(data)/2], "mmdb"); err == nil {
		t.Fatal("truncated MMDB accepted")
	}
}

func TestMMDBCycleRejected(t *testing.T) {
	data, err := os.ReadFile("testdata/chnroutes.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		data[i] = 0
	}
	if _, err := Parse(data, "mmdb"); err == nil {
		t.Fatal("cyclic tree accepted")
	}
}
