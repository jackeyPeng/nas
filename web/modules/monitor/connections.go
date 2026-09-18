package monitor

import (
	"encoding/json"
	"net"
	"net/http"
	"sort"
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

// parseSMBStatusJSON 纯函数：解析 smbstatus --json 输出为连接列表（便于单测）
func parseSMBStatusJSON(out string) []Connection {
	var data struct {
		Sessions map[string]struct {
			User        string `json:"user"`
			Machine     string `json:"machine"`
			Address     string `json:"address"`
			ConnectedAt string `json:"connected_at"`
		} `json:"sessions"`
		Tcons map[string]struct {
			Service     string `json:"service"`
			Machine     string `json:"machine"`
			ConnectedAt string `json:"connected_at"`
		} `json:"tcons"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return nil
	}
	// 每个 session 聚合其共享列表（tcons 按 machine 关联）
	sharesByMachine := map[string][]string{}
	for _, t := range data.Tcons {
		m := normalizeMachine(t.Machine)
		sharesByMachine[m] = append(sharesByMachine[m], t.Service)
	}
	conns := make([]Connection, 0, len(data.Sessions))
	for _, s := range data.Sessions {
		ip := extractIP(s.Address)
		if ip == "" {
			ip = extractIP(s.Machine)
		}
		machine := normalizeMachine(s.Machine)
		shares := sharesByMachine[machine]
		// 若 machine 不是 IP 形式，尝试用 session address 匹配 tcons
		if len(shares) == 0 && ip != "" {
			shares = sharesByMachine[ip]
		}
		sort.Strings(shares)
		conns = append(conns, Connection{
			Protocol:    "smb",
			User:        s.User,
			IP:          ip,
			Detail:      strings.Join(shares, ", "),
			ConnectedAt: s.ConnectedAt,
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
			if v4 := ip.To4(); v4 != nil {
				peerIP = v4.String()
			}
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

// normalizeMachine machine 字段可能是 "ipv4:10.1.2.3:56789" 或主机名，统一成可匹配 key
func normalizeMachine(m string) string {
	m = strings.TrimSpace(m)
	if ip := extractIP(m); ip != "" {
		return ip
	}
	return m
}
