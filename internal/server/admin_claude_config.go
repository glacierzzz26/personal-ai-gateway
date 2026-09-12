package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/secret"
)

// claudeEnv 生成的 settings.json 里的 env 块。字段顺序固定 → 输出稳定,便于用户逐字复制。
type claudeEnv struct {
	BaseURL       string `json:"ANTHROPIC_BASE_URL"`
	AuthToken     string `json:"ANTHROPIC_AUTH_TOKEN"`
	Model         string `json:"ANTHROPIC_MODEL,omitempty"`
	DefaultOpus   string `json:"ANTHROPIC_DEFAULT_OPUS_MODEL,omitempty"`
	DefaultSonnet string `json:"ANTHROPIC_DEFAULT_SONNET_MODEL,omitempty"`
	DefaultHaiku  string `json:"ANTHROPIC_DEFAULT_HAIKU_MODEL,omitempty"`
}

type claudeSettings struct {
	Env claudeEnv `json:"env"`
}

// handleTokenClaudeConfig 生成可直接粘进 ~/.claude/settings.json 的配置(含真实 key)。
// owner 或 admin 可调;老 key(无密文)返回 409。
func (s *Server) handleTokenClaudeConfig(w http.ResponseWriter, r *http.Request) {
	tr, ok := s.loadManageableToken(w, r, "id")
	if !ok {
		return
	}
	if !tr.KeyRetrievable {
		apiErr(w, http.StatusConflict, "key_not_retrievable",
			"该密钥创建于加密存储上线前,无法回显;请删除后重新创建")
		return
	}
	cipher, err := s.st.TokenKeyCipher(tr.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	plain, err := secret.Decrypt(cipher)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	base := s.publicBaseURL(r)
	env, aliases, warnings := s.buildClaudeEnv(tr, plain, base)
	pretty, err := json.MarshalIndent(claudeSettings{Env: env}, "", "  ")
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, domain.ClaudeConfigResp{
		TokenID: tr.ID, BaseURL: base, SettingsJSON: string(pretty),
		ModelAliases: aliases, Warnings: warnings,
	})
}

// publicBaseURL 生成配置里对外可见的网关基址(不含 /v1):优先设置项,否则按请求推导。
func (s *Server) publicBaseURL(r *http.Request) string {
	if st, err := s.st.GetSettings(); err == nil && strings.TrimSpace(st.PublicBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(st.PublicBaseURL), "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := firstCSV(r.Header.Get("X-Forwarded-Proto")); p != "" {
		scheme = p
	}
	host := firstCSV(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

// buildClaudeEnv 挑该令牌可用模型 ∩ 目录中启用且可路由的模型,再按 opus/sonnet/haiku 归类。
// 复用数据面同一语义(engine.SupportsModel),保证生成的模型名一定能转发。
func (s *Server) buildClaudeEnv(tr domain.TokenRead, key, base string) (claudeEnv, map[string]string, []string) {
	env := claudeEnv{BaseURL: base, AuthToken: key}
	aliases := map[string]string{}
	var warnings []string

	var cands []string
	if models, err := s.st.EnabledModelsWithOffers(); err == nil {
		for _, m := range models {
			// 配置里写对外统一名,客户端用统一名请求;令牌规则按统一名或真实名匹配都放行。
			if engine.SupportsModel(tr.AllowedModels, m.Name) || engine.SupportsModel(tr.AllowedModels, m.OriginalName) {
				cands = append(cands, m.Name)
			}
		}
	}
	if len(cands) == 0 {
		warnings = append(warnings, "该令牌当前没有可用模型(检查令牌允许模型与模型目录/供给源启停)")
		return env, aliases, warnings
	}

	opus := pickModel(cands, "opus")
	sonnet := pickModel(cands, "sonnet")
	haiku := pickModel(cands, "haiku")

	// 缺口回退,避免 Claude Code 落到网关没有的模型名。
	if sonnet == "" {
		sonnet = firstNonEmpty(opus, cands[0])
	}
	if opus == "" {
		opus = sonnet
	}
	if haiku == "" {
		haiku = sonnet
	}

	env.Model = sonnet
	env.DefaultOpus = opus
	env.DefaultSonnet = sonnet
	env.DefaultHaiku = haiku
	aliases["opus"] = opus
	aliases["sonnet"] = sonnet
	aliases["haiku"] = haiku
	return env, aliases, warnings
}

// pickModel 返回名字(不区分大小写)含 keyword 的候选中「版本最高」者;无则空串。
func pickModel(cands []string, keyword string) string {
	var hits []string
	for _, c := range cands {
		if strings.Contains(strings.ToLower(c), keyword) {
			hits = append(hits, c)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	sort.Slice(hits, func(i, j int) bool { return versionLess(hits[j], hits[i]) })
	return hits[0]
}

// versionLess 粗略版本比较:逐段比较(数字段按数值,其余按字典序)。
func versionLess(a, b string) bool {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, errA := strconv.Atoi(as[i])
		bn, errB := strconv.Atoi(bs[i])
		if errA == nil && errB == nil {
			if an != bn {
				return an < bn
			}
			continue
		}
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

// splitVersion 按「数字段 / 文本段」切分,便于逐段比较。
func splitVersion(s string) []string {
	var out []string
	var cur strings.Builder
	lastDigit := false
	for i, r := range s {
		isDigit := r >= '0' && r <= '9'
		if i > 0 && isDigit != lastDigit {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
		lastDigit = isDigit
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstCSV 取逗号分隔头的第一个值(反代可能追加多值)。
func firstCSV(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
