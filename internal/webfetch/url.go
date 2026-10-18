package webfetch

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// maxURLChars 封顶 URL 输入（ZCode 同款 2000）。
const maxURLChars = 2000

// NormalizeURL 校验并归一 URL：长度、http(s)、无凭据、主机公网。
// http 一律升级 https（模型可见约定与出站一致——ZCode 同款）。
func NormalizeURL(raw string) (*url.URL, error) {
	if len(raw) > maxURLChars {
		return nil, fmt.Errorf("webfetch: URL 过长（>%d 字符）", maxURLChars)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("webfetch: 无效 URL %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("webfetch: 只支持 http/https（%s）", u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("webfetch: URL 不得携带凭据")
	}
	if u.Scheme == "http" {
		u.Scheme = "https"
		if u.Port() == "80" {
			u.Host = u.Hostname() // 显式 :80 升级后是默认端口，去掉
		}
	}
	host := NormalizeHost(u.Hostname())
	if host == "" {
		return nil, fmt.Errorf("webfetch: URL 缺少主机名")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("webfetch: 需要公网主机名（%s）", host)
	}
	if err := CheckHostEgress(host); err != nil {
		return nil, err
	}
	// 无点的裸主机名（内网机器名形态）拒绝；IP 字面量已过 egress。
	if !strings.Contains(host, ".") && !isIPLiteral(host) {
		return nil, fmt.Errorf("webfetch: 无效主机名（%s）", host)
	}
	return u, nil
}

// isIPLiteral 报告主机名是否 IP 字面量（IPv6 方括号已剥）。
func isIPLiteral(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}

// Redirect 是一次重定向跳转记录。
type Redirect struct {
	From string
	To   string
	Code int
}

// ResolveRedirect 解析 Location（相对形态按当前 URL 解析）。
func ResolveRedirect(location string, current *url.URL) (*url.URL, error) {
	u, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("webfetch: 重定向地址无效 %q", location)
	}
	return current.ResolveReference(u), nil
}

// CheckRedirectHop 校验一跳重定向目标的形态：无凭据、http(s)。egress
// 判定不在此做——跟随循环的每跳顶部统一过（可注入的 Guard），此处硬
// 编码会让测试缝失效（ZCode 同为每跳 preflight）。
func CheckRedirectHop(to *url.URL) error {
	if to.User != nil {
		return errors.New("webfetch: 重定向目标携带凭据，拒绝")
	}
	if to.Scheme != "http" && to.Scheme != "https" {
		return fmt.Errorf("webfetch: 重定向协议不支持（%s）", to.Scheme)
	}
	return nil
}

// SameSiteRedirect 报告该跳是否允许自动跟随（同主机或仅 www 前缀差）。
func SameSiteRedirect(from, to *url.URL) bool {
	if effectivePort(from) != effectivePort(to) {
		return false
	}
	return stripWWW(NormalizeHost(from.Hostname())) == stripWWW(NormalizeHost(to.Hostname()))
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func stripWWW(host string) string { return strings.TrimPrefix(host, "www.") }
