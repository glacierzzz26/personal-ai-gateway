import type { Capability, ChannelType, EgressProto, Provider, QuotaShape } from '@/types';

/** 真实厂商全集(与后端 domain.Providers 一致,勿单独增删)。
 *  已不含 Azure / 聚合中转 —— 前者按区域部署定价不是厂商,后者根本是渠道而非厂商。
 *  commandcode 单页含 20 家模型来源(见 issue #27),后 14 家据此扩容;
 *  厂商归属按 CC 详情页 JSON-LD 的 brand 逐模型核对(LongCat→Meituan)。 */
export const providers: Provider[] = [
  'OpenAI', 'Anthropic', 'DeepSeek', '通义千问', '智谱', 'Moonshot',
  'Google', 'xAI', 'Xiaomi', 'Meta', 'MiniMax',
  'NVIDIA', 'Tencent', 'StepFun', 'Meituan',
  'Thinking Machines', 'Sakana AI', 'Poolside', 'InclusionAI', 'Jev',
];

/** 渠道类型全集(与后端 domain.ChannelTypes 一致)。决定上游额度怎么查。
 *  mark 是 provider 为空时 ProviderMark 的替代字形。 */
export const channelTypes: Array<{ value: ChannelType; label: string; mark: string; desc: string }> = [
  { value: 'deepseek', label: 'DeepSeek 官方', mark: 'D', desc: '官方 /user/balance 余额' },
  { value: 'commandcode', label: 'command code', mark: 'CC', desc: 'credits + 5h/周 窗口' },
  { value: 'opencode', label: 'opencode', mark: 'OC', desc: 'base_url/v1/usage 窗口' },
  { value: 'thirdparty', label: '第三方', mark: '三方', desc: '人工配置额度路径与形状' },
];

/** 出站协议全集(与后端 domain.EgressProtos 一致)。决定请求怎么发上去。 */
export const egressProtos: Array<{ value: EgressProto; label: string }> = [
  { value: 'openai', label: 'OpenAI 兼容' },
  { value: 'anthropic', label: 'Anthropic 原生' },
  { value: 'azure', label: 'Azure(带 api-version)' },
];

/** 第三方渠道的额度响应形状(与后端 domain.QuotaShapes 一致)。 */
export const quotaShapes: Array<{ value: QuotaShape; label: string; hint: string }> = [
  { value: 'usage', label: '通用额度信封', hint: '/v1/usage → {usage:{rolling,weekly,monthly}}' },
  { value: 'oneapi', label: 'one-api / new-api 订阅', hint: '/v1/dashboard/billing/subscription + /usage' },
  { value: 'newapi_user', label: 'new-api 用户额度', hint: '/api/user/self → {data:{quota,used_quota}}' },
];

/** 模型能力全集 */
export const capabilities: Capability[] = ['vision', 'function', 'stream', 'reasoning'];
