package proxy

import (
	"net/http"
	"strings"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
)

// 本文件:a2o 推理回填的「缓存未命中自愈」。
//
// 背景(见 DESIGN §5.2):thinking 上游在请求带 tools 时要求所有历史 assistant 消息
// 回传 reasoning_content,缺失即 400:
//
//	The `reasoning_content` in the thinking mode must be passed back to the API.
//
// 网关靠 reasonCache 记住上一轮的 reasoning 再补回,但缓存可能未命中:
// 进程重启、超过 TTL、历史不是网关产生的、上游该轮 reasoning 为空/字段形态不同等。
// 此时按旧逻辑只能把 400 原样抛给客户端,而且该段历史会一直留在对话里,
// 后续每一轮都继续 400 —— 对话就此卡死。
//
// 实测上游只校验「字段存在」,空串即可通过(见 DESIGN §5.2「空串自愈」)。
// 因此遇到这个特定 400 时,同渠道用 forceReasoning 重建请求重发一次:
// 命中缓存仍用真实 reasoning,未命中写 ""。重发成功则对话继续,
// 本轮成功响应又会把真实 reasoning 写回缓存。

// isReasoningBackfill400 判定上游 400 是否为「thinking 模式必须回传 reasoning_content」。
// 只认这个特定成因:空串重发对别的 400 没有意义,还会白白多打一次上游。
func isReasoningBackfill400(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	s := string(body)
	return strings.Contains(s, "reasoning_content") && strings.Contains(s, "must be passed back")
}

// canHealReasoning 只有「anthropic 入站 → openai 出站」的重建路径才有 reasoning_content 概念。
func canHealReasoning(inProto, outProto string, status int, body []byte) bool {
	return inProto == ProtoAnthropic && outProto == ProtoOpenAI &&
		isReasoningBackfill400(status, body)
}

// rebuildWithForcedReasoning 用空串兜底重建同一次出站请求(仅 a2o)。
func (g *Gateway) rebuildWithForcedReasoning(ch domain.ChannelRow, inProto, outProto string, in *inboundReq, plan *engine.Plan, at engine.Attempt, stream bool) (*outboundReq, error) {
	return buildOutbound(ch, inProto, outProto, in.op, in.body, stream,
		g.reason.ForToken(in.token.ID), true, outboundModel(plan, at), in.sessionID)
}
