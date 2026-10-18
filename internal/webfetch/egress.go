// Package webfetch implements the webfetch tool's fetch-and-extract
// pipeline（ZCode 的 WebFetch 子系统）：URL 归一、每跳 egress 守卫
// （SSRF：字面量本地/私网目标阻断）、重定向策略（同主机才跟随）、
// 文本类内容抽取与封顶、短 TTL 缓存。
package webfetch

import (
	"fmt"
	"net/netip"
	"strings"
)

// IPv4 非公网段（RFC1918/环回/链路本地/CGNAT/组播/保留等）——Go 无
// ipaddr.js 的 range() 分类，显式列出 CIDR（ZCode：unicast 之外的段
// 加 198.18/15 benchmark）。
var blockedIPv4 = mustCIDRs(
	"0.0.0.0/8",       // "this network"
	"10.0.0.0/8",      // RFC1918 私网
	"100.64.0.0/10",   // CGNAT（运营商级 NAT，同样非公网可达）
	"127.0.0.0/8",     // 环回
	"169.254.0.0/16",  // 链路本地（云元数据服务 169.254.169.254）
	"172.16.0.0/12",   // RFC1918 私网
	"192.0.0.0/24",    // IETF 协议指派
	"192.0.2.0/24",    // TEST-NET-1
	"192.168.0.0/16",  // RFC1918 私网
	"198.18.0.0/15",   // 网络互联设备 benchmark
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // 组播
	"240.0.0.0/4",     // 保留（含 255.255.255.255 广播）
)

// IPv6 非普通公网段（环回/唯一本地/链路本地/组播/文档前缀等）。全球
// 单播之外的地址全部拒绝。
var blockedIPv6 = mustCIDRs(
	"::/128",         // 未指定
	"::1/128",        // 环回
	"64:ff9b:1::/48", // special-use（Local-Use IPv4/IPv6 转换）
	"100::/64",       // 丢弃前缀
	"2001:2::/48",    // benchmark
	"2001:10::/28",   // ORCHID（已废弃）
	"2001:20::/28",   // ORCHIDv2
	"2001:db8::/32",  // 文档示例
	"2002::/16",      // 6to4（可携带任意 IPv4，含私网——一并拒绝）
	"fc00::/7",       // 唯一本地（ULA）
	"fe80::/10",      // 链路本地
	"ff00::/8",       // 组播
)

// dns64Prefix 是 NAT64/DNS64 的 well-known 前缀 64:ff9b::/96——低 32 位
// 编码 IPv4 地址，必须还原后套用同一套公网判定（否则 ::ffff:10.0.0.1
// 之类可以绕过）。IPv4-mapped（::ffff:0:0/96）由 netip 的 4in6 转换处理。
var dns64Prefix = mustPrefix("64:ff9b::/96")

// mustCIDRs 解析 CIDR 列表（初始化期失败即 panic——静态数据错误）。
func mustCIDRs(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			panic("webfetch: bad CIDR " + c + ": " + err.Error())
		}
		out = append(out, p.Masked())
	}
	return out
}

func mustPrefix(cidr string) netip.Prefix {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		panic("webfetch: bad CIDR " + cidr + ": " + err.Error())
	}
	return p.Masked()
}

// NormalizeHost 归一主机名形态（去括号/尾点/小写）。
func NormalizeHost(hostname string) string {
	h := strings.ToLower(strings.TrimSpace(hostname))
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	return strings.TrimSuffix(h, ".")
}

// IsLocalHostname 报告 localhost 家族名。
func IsLocalHostname(hostname string) bool {
	h := NormalizeHost(hostname)
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

// CheckHostEgress 校验主机名的字面量层 egress：localhost 家族与本地/
// 私网/特殊用途 IP 字面量一律拒绝。普通域名不做 DNS 预解析（ZCode 同
// 款取舍：部分网络下 DNS preflight 1s 内无法完成会让公网 URL 在真实
// fetch 前失败；且重定向每跳都过这道门）。
func CheckHostEgress(hostname string) error {
	h := NormalizeHost(hostname)
	if IsLocalHostname(h) {
		return fmt.Errorf("webfetch: 私网或本地主机名被拒绝（%s）", h)
	}
	addr, err := netip.ParseAddr(h)
	if err != nil {
		return nil // 普通域名：字面量层无判定
	}
	return CheckAddrEgress(addr)
}

// CheckAddrEgress 判定 IP 地址是否公网可达（IPv4-mapped/DNS64 先还原
// 成 IPv4 再套同一套判定）。
func CheckAddrEgress(addr netip.Addr) error {
	if addr.Is4In6() {
		addr = addr.Unmap()
	} else if dns64Prefix.Contains(addr) {
		// 64:ff9b::/96 的低 32 位是内嵌 IPv4——还原判定。
		b := addr.As16()
		embedded := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		addr = embedded
	}
	if addr.Is4() {
		for _, p := range blockedIPv4 {
			if p.Contains(addr) {
				return fmt.Errorf("webfetch: 私网或本地 IP 被拒绝（%s 命中 %s）", addr, p)
			}
		}
		return nil
	}
	for _, p := range blockedIPv6 {
		if p.Contains(addr) {
			return fmt.Errorf("webfetch: 非公网 IPv6 被拒绝（%s 命中 %s）", addr, p)
		}
	}
	return nil
}
