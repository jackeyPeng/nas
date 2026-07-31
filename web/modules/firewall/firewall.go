package firewall

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"nas-panel/common"
)

// FirewallRule 一条 UFW 规则
type FirewallRule struct {
	Num     int    `json:"num"`     // 规则编号（删除用）
	Port    string `json:"port"`    // 22 或 30000:31000
	Proto   string `json:"proto"`   // tcp/udp/any
	Action  string `json:"action"`  // allow/deny
	From    string `json:"from"`    // Anywhere 或 IP
	V6      bool   `json:"v6"`      // 是否 IPv6 规则
	Comment string `json:"comment"` // 备注
}

// FirewallStatus 防火墙整体状态
type FirewallStatus struct {
	Installed       bool           `json:"installed"`
	Active          bool           `json:"active"`
	DefaultIncoming string         `json:"default_incoming"` // deny/allow/reject
	DefaultOutgoing string         `json:"default_outgoing"`
	Rules           []FirewallRule `json:"rules"`
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/firewall", common.AuthMiddleware(handleStatus))
	mux.HandleFunc("POST /api/firewall/rules", common.AuthMiddleware(handleAddRule))
	mux.HandleFunc("DELETE /api/firewall/rules/{num}", common.AuthMiddleware(handleDeleteRule))
	mux.HandleFunc("POST /api/firewall/enable", common.AuthMiddleware(handleEnable))
	mux.HandleFunc("POST /api/firewall/disable", common.AuthMiddleware(handleDisable))
}

// ---------- 状态解析 ----------

func handleStatus(w http.ResponseWriter, r *http.Request) {
	common.JSONResponse(w, getStatus())
}

func getStatus() FirewallStatus {
	st := FirewallStatus{Rules: []FirewallRule{}}

	// 检查 ufw 是否安装
	if _, err := common.ExecOutput("which", "ufw"); err != nil {
		return st // installed=false
	}
	st.Installed = true

	out, _ := common.SudoOutput("ufw", "status", "numbered")
	if strings.Contains(out, "Status: active") {
		st.Active = true
	}

	// 默认策略（ufw status verbose 才有，单独取）
	verbose, _ := common.SudoOutput("ufw", "status", "verbose")
	for _, line := range strings.Split(verbose, "\n") {
		if strings.HasPrefix(line, "Default:") {
			// 格式: Default: deny (incoming), allow (outgoing), disabled (routed)
			re := regexp.MustCompile(`(\w+) \(incoming\), (\w+) \(outgoing\)`)
			if m := re.FindStringSubmatch(line); len(m) == 3 {
				st.DefaultIncoming = m[1]
				st.DefaultOutgoing = m[2]
			}
		}
	}

	st.Rules = parseRules(out)
	return st
}

// parseRules 解析 ufw status numbered 输出
// 行格式: [ 1] 22/tcp                     ALLOW IN    Anywhere
//         [18] 22/tcp (v6)               ALLOW IN    Anywhere (v6)
var ruleRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(\S+?)(?:\s+\(v6\))?\s+(ALLOW|DENY|LIMIT|REJECT)\s+(?:IN|OUT)\s+(.+?)(?:\s+\(v6\))?\s*(?:#.*)?$`)

func parseRules(out string) []FirewallRule {
	rules := []FirewallRule{}
	for _, line := range strings.Split(out, "\n") {
		m := ruleRe.FindStringSubmatch(strings.TrimRight(line, " "))
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		portProto := m[2]
		v6 := strings.Contains(line, "(v6)")
		action := strings.ToLower(m[3])
		if action == "limit" {
			action = "allow" // LIMIT 本质是限速放行
		}
		from := strings.TrimSpace(m[4])
		from = strings.TrimSuffix(from, "(v6)")
		from = strings.TrimSpace(from)

		port := portProto
		proto := "any"
		if idx := strings.LastIndex(portProto, "/"); idx > 0 {
			port = portProto[:idx]
			proto = portProto[idx+1:]
		}

		rules = append(rules, FirewallRule{
			Num:    num,
			Port:   port,
			Proto:  proto,
			Action: action,
			From:   from,
			V6:     v6,
		})
	}
	return rules
}

// ---------- 规则操作 ----------

var portRe = regexp.MustCompile(`^\d{1,5}(:\d{1,5})?$`)
var ipRe = regexp.MustCompile(`^[\d\./:a-fA-F]+$`)

func handleAddRule(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	port := strings.TrimSpace(r.FormValue("port"))
	proto := strings.TrimSpace(r.FormValue("proto"))
	action := strings.TrimSpace(r.FormValue("action"))
	from := strings.TrimSpace(r.FormValue("from"))
	comment := strings.TrimSpace(r.FormValue("comment"))

	if port == "" {
		http.Error(w, `{"error":"端口号必填"}`, http.StatusBadRequest)
		return
	}
	if !portRe.MatchString(port) {
		http.Error(w, `{"error":"端口格式无效，支持单端口(22)或范围(30000:31000)"}`, http.StatusBadRequest)
		return
	}
	if proto != "tcp" && proto != "udp" && proto != "any" {
		proto = "tcp"
	}
	if action != "deny" {
		action = "allow"
	}
	if from != "" && from != "any" && !ipRe.MatchString(from) {
		http.Error(w, `{"error":"来源 IP 格式无效"}`, http.StatusBadRequest)
		return
	}

	args := []string{}
	if action == "deny" {
		args = append(args, "deny")
	} else {
		args = append(args, "allow")
	}
	if from != "" && from != "any" {
		args = append(args, "from", from, "to", "any", "port", strings.Split(port, ":")[0])
		if proto != "any" {
			args = append(args, "proto", proto)
		}
	} else {
		if proto == "any" {
			args = append(args, port)
		} else {
			args = append(args, port+"/"+proto)
		}
	}
	if comment != "" {
		args = append(args, "comment", comment)
	}

	out, err := common.SudoExec("ufw", args...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("规则已添加: %s %s/%s", map[string]string{"allow": "允许", "deny": "拒绝"}[action], port, proto),
	})
}

func handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	numStr := r.PathValue("num")
	num, err := strconv.Atoi(numStr)
	if err != nil || num <= 0 {
		http.Error(w, `{"error":"无效的规则编号"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("ufw", "--force", "delete", strconv.Itoa(num))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": fmt.Sprintf("规则 #%d 已删除", num)})
}

func handleEnable(w http.ResponseWriter, r *http.Request) {
	// --force 避免交互确认；启用前确保 22 和 8090 放行，防止把自己锁外面
	common.SudoExec("ufw", "allow", "22/tcp")
	common.SudoExec("ufw", "allow", "8090/tcp")
	out, err := common.SudoExec("ufw", "--force", "enable")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": "防火墙已启用（已自动放行 SSH 22 和面板 8090）"})
}

func handleDisable(w http.ResponseWriter, r *http.Request) {
	out, err := common.SudoExec("ufw", "disable")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": "防火墙已禁用"})
}
