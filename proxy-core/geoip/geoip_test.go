package geoip

import "testing"

func TestParseRegion(t *testing.T) {
	cases := []struct {
		region string
		want   Location
	}{
		{"中国|0|广东省|深圳市|电信", Location{Country: "中国", Province: "广东省", City: "深圳市", ISP: "电信"}},
		{"美国|0|0|0|0", Location{Country: "美国"}},
		{"", Location{}},
		{"日本|0|0|东京都|0", Location{Country: "日本", City: "东京都"}},
	}
	for _, c := range cases {
		if got := parseRegion(c.region); got != c.want {
			t.Errorf("parseRegion(%q) = %+v, want %+v", c.region, got, c.want)
		}
	}
}

func TestLocationString(t *testing.T) {
	cases := []struct {
		loc  Location
		want string
	}{
		{Location{Country: "中国", Province: "广东省", City: "深圳市"}, "中国 · 广东省 · 深圳市"},
		{Location{Country: "美国"}, "美国"},
		// 直辖市省份与城市同名时合并，避免"北京市 · 北京市"。
		{Location{Country: "中国", Province: "北京市", City: "北京市"}, "中国 · 北京市"},
		{Location{}, ""},
	}
	for _, c := range cases {
		if got := c.loc.String(); got != c.want {
			t.Errorf("Location%+v.String() = %q, want %q", c.loc, got, c.want)
		}
	}
}

// 以下为离线查询测试（纯本地，不依赖网络）。
// 测试使用的 IP 段在 ip2region 数据中须稳定命中，断言只做国家级宽松校验。

func TestLookupKnownIP(t *testing.T) {
	cases := []struct {
		ip      string
		country string
	}{
		// ip2region 海外数据使用英文国家名，国内数据使用中文。
		{"8.8.8.8", "United States"},
		{"220.181.38.148", "中国"},
		{"114.114.114.114", "中国"},
	}
	for _, c := range cases {
		loc, ok := Lookup(c.ip)
		if !ok {
			t.Errorf("Lookup(%s) = not found, want country %s", c.ip, c.country)
			continue
		}
		if loc.Country != c.country {
			t.Errorf("Lookup(%s) country = %q, want %q (full: %+v)", c.ip, loc.Country, c.country, loc)
		}
	}
}

func TestLookupNegative(t *testing.T) {
	// 非 IP 字符串
	if _, ok := Lookup("not-an-ip"); ok {
		t.Error("Lookup(non-ip) should be not found")
	}
	// 空字符串
	if _, ok := Lookup(""); ok {
		t.Error("Lookup(empty) should be not found")
	}
	// 内网/回环地址在 ip2region 库中标为 Reserved（保留地址段），仍算命中。
	for _, ip := range []string{"192.168.1.1", "127.0.0.1", "10.0.0.5"} {
		loc, ok := Lookup(ip)
		if !ok {
			t.Errorf("Lookup(%s) should be found as reserved address", ip)
			continue
		}
		if loc.Country != "Reserved" {
			t.Errorf("Lookup(%s) country = %q, want Reserved", ip, loc.Country)
		}
	}
}