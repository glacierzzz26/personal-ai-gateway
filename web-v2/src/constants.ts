import type { Capability, Provider } from '@/types';

/** 渠道供应商全集(与后端 domain.Providers 一致,勿单独增删) */
export const providers: Provider[] = [
  'OpenAI', 'Anthropic', 'Azure', 'DeepSeek',
  '通义千问', '智谱', 'Moonshot', '聚合中转',
];

/** 模型能力全集 */
export const capabilities: Capability[] = ['vision', 'function', 'stream', 'reasoning'];
