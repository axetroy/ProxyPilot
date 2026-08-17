// Package geoip 提供完全离线的 IP 地理位置查询（国家/省份/城市/运营商）。
//
// 数据来源为 ip2region（Apache-2.0）的 IPv4 xdb 数据文件，通过 go:embed
// 随二进制一起编译，不依赖任何外部 API 或运行时文件，天然适配无 CGO 的
// 交叉编译（goreleaser 六目标）。
//
// 查询为全内存加载（10 微秒级），单例并发安全；IPv6 数据文件过大（36MB+）
// 且代理节点几乎全为 IPv4，故暂不内置 IPv6 数据，IPv6 地址查询返回未命中。
package geoip

import (
	_ "embed"
	"fmt"
	"net"
	"sync"

	"github.com/admpub/ip2region/v3/binding/golang/xdb"
)

//go:embed data/ip2region_v4.xdb
var v4Data []byte

// Location 表示一个 IP 的地理位置。
// ip2region 的 region 格式为"国家|区域|省份|城市|运营商"，部分字段可能为 "0" 或空。
type Location struct {
	Country  string `json:"country,omitempty"`  // 国家，如"中国"、"美国"
	Province string `json:"province,omitempty"` // 省份/州/区划，如"广东省"
	City     string `json:"city,omitempty"`     // 城市，如"深圳市"
	ISP      string `json:"isp,omitempty"`      // 运营商，如"电信"
}

// String 返回便于展示的组合文案：国家 · 省份 · 城市，
// 连续重复的段（如直辖市省份与城市同名）自动合并。
func (l Location) String() string {
	out := ""
	last := ""
	for _, v := range []string{l.Country, l.Province, l.City} {
		if v == "" || v == last {
			continue
		}
		if out == "" {
			out = v
		} else {
			out += " · " + v
		}
		last = v
	}
	return out
}

var (
	lookupOnce sync.Once
	searcher   *xdb.Searcher
	initErr    error
)

func initSearcher() {
	searcher, initErr = xdb.NewWithBuffer(xdb.IPv4, v4Data)
	if initErr != nil {
		initErr = fmt.Errorf("geoip: load ip2region v4 dataset: %w", initErr)
	}
}

// Lookup 查询单个 IP 的地理位置。ip 必须是不带端口的纯 IP 字符串
// （IPv4 或 IPv6，IPv6 当前无数据返回未命中）。
// 返回的 bool 表示是否命中（命中即可信，未命中时 Location 为空值）。
func Lookup(ip string) (Location, bool) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return Location{}, false
	}
	lookupOnce.Do(initSearcher)
	if initErr != nil || searcher == nil {
		return Location{}, false
	}
	region, err := searcher.SearchByStr(parsed.String())
	if err != nil || region == "" {
		return Location{}, false
	}
	return parseRegion(region), true
}

// LookupHost 查询一个 host 的地理位置：host 可以是 IP 字符串或域名。
// 域名会做一次 DNS 解析（优先 IPv4 地址）；解析失败时返回未命中。
// 注意：LookupHost 依赖 DNS，测试环境不要用它，避免真实联网。
func LookupHost(host string) (Location, bool) {
	if loc, ok := Lookup(host); ok {
		return loc, true
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return Location{}, false
	}
	for _, addr := range addrs {
		if ip := addr.String(); ip != "" {
			if loc, ok := Lookup(ip); ok {
				return loc, true
			}
		}
	}
	return Location{}, false
}

// parseRegion 把 ip2region 的 region 字符串解析成 Location。
// 格式：国家|区域|省份|城市|运营商，未知字段为 "0" 或空串。
func parseRegion(region string) Location {
	var loc Location
	parts := splitRegion(region)
	clean := func(v string) string {
		if v == "" || v == "0" {
			return ""
		}
		return v
	}
	if len(parts) > 0 {
		loc.Country = clean(parts[0])
	}
	// parts[1] 是"区域"（通常为 0，代表全国），暂不单独暴露。
	if len(parts) > 2 {
		loc.Province = clean(parts[2])
	}
	if len(parts) > 3 {
		loc.City = clean(parts[3])
	}
	if len(parts) > 4 {
		loc.ISP = clean(parts[4])
	}
	return loc
}

// splitRegion 把 region 按 "|" 分割，忽略尾部可能的空段。
func splitRegion(region string) []string {
	parts := make([]string, 0, 5)
	start := 0
	for i := 0; i <= len(region); i++ {
		if i == len(region) || region[i] == '|' {
			parts = append(parts, region[start:i])
			start = i + 1
		}
	}
	return parts
}