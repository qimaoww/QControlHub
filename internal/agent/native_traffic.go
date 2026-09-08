//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
)

// Both cores expose the same read-only QueryStats wire shape. Keep the small
// interoperable codec here instead of linking either proxy core into qagent.
// The empty request has reset=false; collecting NEVER clears core counters.
type nativeStatsCodec struct{}

func (nativeStatsCodec) Name() string { return "proto" }
func (nativeStatsCodec) Marshal(value any) ([]byte, error) {
	if _, ok := value.(*nativeStatsRequest); !ok {
		return nil, errors.New("unexpected stats request")
	}
	return nil, nil
}
func (nativeStatsCodec) Unmarshal(data []byte, value any) error {
	result, ok := value.(*nativeStatsResponse)
	if !ok {
		return errors.New("unexpected stats response")
	}
	result.Counters = make(map[string]uint64)
	for len(data) > 0 {
		num, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		if num != 1 {
			n = protowire.ConsumeFieldValue(num, kind, data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			continue
		}
		if kind != protowire.BytesType {
			return errors.New("invalid stats entry type")
		}
		entry, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		name, count, err := decodeNativeStat(entry)
		if err != nil {
			return err
		}
		if _, exists := result.Counters[name]; exists {
			return errors.New("duplicate core counter")
		}
		if len(result.Counters) >= 16384 {
			return errors.New("too many core counters")
		}
		result.Counters[name] = count
	}
	return nil
}

type nativeStatsRequest struct{}
type nativeStatsResponse struct{ Counters map[string]uint64 }

func decodeNativeStat(data []byte) (string, uint64, error) {
	var name string
	var count uint64
	for len(data) > 0 {
		num, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return "", 0, protowire.ParseError(n)
		}
		data = data[n:]
		switch num {
		case 1:
			if kind != protowire.BytesType {
				return "", 0, errors.New("invalid counter name type")
			}
			name, n = protowire.ConsumeString(data)
		case 2:
			if kind != protowire.VarintType {
				return "", 0, errors.New("invalid counter value type")
			}
			count, n = protowire.ConsumeVarint(data)
		default:
			n = protowire.ConsumeFieldValue(num, kind, data)
		}
		if n < 0 {
			return "", 0, protowire.ParseError(n)
		}
		data = data[n:]
	}
	if name == "" || len(name) > 512 || count > math.MaxInt64 {
		return "", 0, errors.New("invalid core counter")
	}
	return name, count, nil
}

func queryNativeTraffic(ctx context.Context, engine core.Engine) (map[string]uint64, error) {
	return queryNativeTrafficAt(ctx, engine, "")
}

func queryNativeTrafficAt(ctx context.Context, engine core.Engine, verifiedAddress string) (map[string]uint64, error) {
	var address, service string
	switch engine {
	case core.EngineXray:
		address, service = "127.0.0.1:10085", "xray.app.stats.command.StatsService"
	case core.EngineSingBox:
		// sing-box overrides its generated descriptor name at runtime for
		// compatibility with V2Ray clients (experimental/v2rayapi/stats.go).
		address, service = "127.0.0.1:10086", "v2ray.core.app.stats.command.StatsService"
	default:
		return nil, errors.New("native traffic API not supported")
	}
	if verifiedAddress != "" {
		host, port, err := net.SplitHostPort(verifiedAddress)
		n, _ := strconv.Atoi(port)
		if err != nil || (host != "127.0.0.1" && host != "::1") || n < 1 || n > 65535 {
			return nil, errors.New("statistics API must use a verified loopback TCP address")
		}
		address = verifiedAddress
	}
	// Never dial a control-plane/user-supplied host: these endpoints must be
	// loopback-only and belong to a verified managed process.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	connection, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	var result nativeStatsResponse
	err = connection.Invoke(ctx, "/"+service+"/QueryStats", &nativeStatsRequest{}, &result, grpc.ForceCodec(nativeStatsCodec{}), grpc.MaxCallRecvMsgSize(4<<20))
	if err != nil {
		return nil, fmt.Errorf("%s statistics: %w", engine, err)
	}
	return result.Counters, nil
}

type trafficLegs struct {
	ClientReceived uint64 `json:"client_received"`
	ClientSent     uint64 `json:"client_sent"`
	TargetReceived uint64 `json:"target_received"`
	TargetSent     uint64 `json:"target_sent"`
}

func nativeTrafficLegs(counters map[string]uint64, port serverconfig.AccountingPort) trafficLegs {
	legs := trafficLegs{ClientReceived: counters["inbound>>>"+port.Inbound+">>>traffic>>>uplink"], ClientSent: counters["inbound>>>"+port.Inbound+">>>traffic>>>downlink"]}
	for _, tag := range port.Outbounds {
		legs.TargetSent = saturatedTrafficAdd(legs.TargetSent, counters["outbound>>>"+tag+">>>traffic>>>uplink"])
		legs.TargetReceived = saturatedTrafficAdd(legs.TargetReceived, counters["outbound>>>"+tag+">>>traffic>>>downlink"])
	}
	return legs
}
