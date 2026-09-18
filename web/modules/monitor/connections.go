package monitor

import (
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"nas-panel/common"
)

// ═══ 当前连接（TODO #31）═══
// 聚合 SMB(smbstatus) + SSH/NFS/FTP/WebDAV/S3(ss 按端口归类) 的活跃会话。

// Connection 一条活跃连接
type Connection struct {
	Protocol    string `json:"protocol"`     // smb/ssh/nfs/ftp/webdav/s3/web
	User        string `json:"user"`         // 用户名（仅 SMB/SSH 可知）
	IP          string `json:"ip"`           // 客户端 IP
	Detail      string `json:"detail"`       // 共享名/终端等补充信息
	ConnectedAt string `json:"connected_at"` // 连接时间（仅 SMB 可提供，ISO 格式）
}

// 本地监听端口 → 协议映射（面板固定端口：WebDAV 8080 / S3 9000 / 面板 8090）
var portProtocols = map[string]string{
	"22":   "ssh",
	"21":   "ftp",
	"445":  "smb",
	"139":  "smb",
	"2049": "nfs",
	"8080": "webdav",
	"9000": "s3",
	"8090": "web",
	"8081": "web", // FileBrowser
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conns := getConnections()
	common.JSONResponse(w, map[string]interface{}{
		"connections": conns,
		"total":       len(conns),
	})
}

func getConnections() []Connection {
	conns := make([]Connection, 0)
	// SMB 会话优先用 smbstatus（有用户名/共享/连接时间）
	smb := getSMBConnections()
	smbIPSeen := make(map[string]bool)
	for _, c := range smb {
		smbIPSeen[c.IP+c.Detail] = true
		conns = append(conns, c)
	}
	// 其余协议按 ss 端口归类
	for _, c := range getTCPConnections() {
		if c.Protocol == "smb" {
			// smbstatus 已给出更完整信息；仅在 smbstatus 失败(无 SMB 条目)时兜底
			if len(smb) > 0 {
				continue
			}
		}
		conns = append(conns, c)
	}
	// SSH 用户名补充：who 输出（IP → user）
	for _, u := range getLoggedInUsers() {
		ip := u["from"]
		if ip == "" {
			continue
		}
		for i := range conns {
			if conns[i].Protocol == "ssh" && conns[i].IP == ip && conns[i].User == "" {
				conns[i].User = u["user"]
				if conns[i].Detail == "" {
					conns[i].Detail = u["tty"]
				}
			}
		}
	}
	sort.Slice(conns, func(i, j int) bool {
		if conns[i].Protocol != conns[j].Protocol {
			return conns[i].Protocol < conns[j].Protocol
		}
		return conns[i].IP < conns[j].IP
	})
	return conns
}

// getSMBConnections 解析 smbstatus --json（Samba 4.x）
func getSMBConnections() []Connection {
	out, err := common.SudoOutput("smbstatus", "--json")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	return parseSMBStatusJSON(out)
}

// parseSMBStatusJSON 纯函数：解析 smbstatus --json 输出为连接列表（便于单测）。
// 实测 schema（Samba 4.22）：sessions 键为 session_id，字段 username/remote_machine/hostname/auth_time；
// tcons 带 session_id 可直接关联共享列表，不用按 machine 猜。
func parseSMBStatusJSON(out string) []Connection {
	var data struct {
		Sessions map[string]struct {
			Username      string `json:"username"`
			RemoteMachine string `json:"remote_machine"`
			Hostname      string `json:"hostname"`
			AuthTime      string `json:"auth_time"`
		} `json:"sessions"`
		Tcons map[string]struct {
			Service     string `json:"service"`
			SessionID   string `json:"session_id"`
			ConnectedAt string `json:"connected_at"`
		} `json:"tcons"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return nil
	}
	// tcons 按 session_id 聚合共享列表
	sharesBySession := map[string][]string{}
	for _, t := range data.Tcons {
		sharesBySession[t.SessionID] = append(sharesBySession[t.SessionID], t.Service)
	}
	conns := make([]Connection, 0, len(data.Sessions))
	for sid, s := range data.Sessions {
		ip := normalizeClientIP(s.RemoteMachine)
		if ip == "" {
			ip = normalizeClientIP(s.Hostname)
		}
		if ip == "127.0.0.1" || ip == "::1" {
			continue // 本机回环不算客户端连接（与 ss 路径一致）
		}
		shares := sharesBySession[sid]
		sort.Strings(shares)
		conns = append(conns, Connection{
			Protocol:    "smb",
			User:        s.Username,
			IP:          ip,
			Detail:      strings.Join(shares, ", "),
			ConnectedAt: s.AuthTime,
		})
	}
	return conns
}

// getTCPConnections 用 ss 列出已建立连接并按本地端口归类协议
func getTCPConnections() []Connection {
	out, err := common.ExecOutput("ss", "-Htn", "state", "established")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	conns := make([]Connection, 0)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// ss -Htn 输出: Recv-Q Send-Q LocalAddr:Port PeerAddr:Port
		local, peer := fields[2], fields[3]
		_, localPort, err := net.SplitHostPort(local)
		if err != nil {
			continue
		}
		peerIP, _, err := net.SplitHostPort(peer)
		if err != nil {
			continue
		}
		// IPv4-mapped IPv6（::ffff:1.2.3.4）归一为纯 IPv4
		if ip := net.ParseIP(peerIP); ip != nil {
			peerIP = canonicalIP(ip)
		}
		if peerIP == "127.0.0.1" || peerIP == "::1" {
			continue // 本机回环不算客户端连接
		}
		proto, ok := portProtocols[localPort]
		if !ok {
			continue
		}
		conns = append(conns, Connection{Protocol: proto, IP: peerIP})
	}
	return conns
}

// extractIP 从 "ipv4:10.1.2.3:45678" 或 "10.1.2.3:45678" 提取纯 IP
func extractIP(addr string) string {
	addr = strings.TrimSpace(addr)
	// 先剥 smbstatus 的 ipv4:/ipv6: 前缀（含前缀时 SplitHostPort 会因多冒号失败）
	addr = strings.TrimPrefix(addr, "ipv4:")
	addr = strings.TrimPrefix(addr, "ipv6:")
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	if ip := net.ParseIP(addr); ip != nil {
		return addr
	}
	return ""
}

// normalizeClientIP 提取客户端 IP 并做 IPv4-mapped 归一。
// 覆盖 smbstatus 实测形态："::1"、"ipv6:::1:54770"（=host ::1 + port 54770，
// 剥前缀后整串还能被 ParseIP 误认成合法 IPv6，必须先剥末尾 :port）、
// "ipv4:10.1.2.3:54770"、"10.1.2.3"、主机名（返回 ""）。
func normalizeClientIP(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "ipv4:")
	addr = strings.TrimPrefix(addr, "ipv6:")
	// 直接是 IP
	if ip := net.ParseIP(addr); ip != nil {
		return canonicalIP(ip)
	}
	// host:port（IPv4 或 [IPv6]:port）
	if h, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(h); ip != nil {
			return canonicalIP(ip)
		}
		return ""
	}
	// IPv6 带端口但无方括号（smbstatus ipv6: 前缀剥离后的形态）：
	// 剥掉最后一个冒号段（若为纯数字端口）再试
	if i := strings.LastIndex(addr, ":"); i > 0 {
		if _, err := strconv.Atoi(addr[i+1:]); err == nil {
			if ip := net.ParseIP(addr[:i]); ip != nil {
				return canonicalIP(ip)
			}
		}
	}
	return ""
}

// canonicalIP IPv4-mapped IPv6（::ffff:1.2.3.4）归一为纯 IPv4
func canonicalIP(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}

// normalizeMachine machine 字段可能是 "ipv4:10.1.2.3:56789" 或主机名，统一成可匹配 key
func normalizeMachine(m string) string {
	m = strings.TrimSpace(m)
	if ip := extractIP(m); ip != "" {
		return ip
	}
	return m
}
