import type { Channel, ChannelType, EgressProto, Provider } from '@/types';
import { channelTypes, egressProtos } from '@/constants';

/** 渠道类型元数据(标签 / 备选字形)。未知值回落原始串。 */
export function channelTypeOf(t: ChannelType | '' | undefined) {
  return channelTypes.find(c => c.value === t);
}

/** 出站协议中文标签。未知值回落原始串。 */
export function egressLabel(p: EgressProto | '' | undefined): string {
  if (!p) return '';
  return egressProtos.find(e => e.value === p)?.label ?? p;
}

/**
 * 渠道显示名:有单一厂商时显示厂商,否则回落渠道类型标签
 * (command code / opencode 这类聚合渠道 provider 为空 —— 显示类型比显示空更说明问题)。
 */
export function channelLabel(ch: Pick<Channel, 'provider' | 'channelType'> | { provider: Provider; channelType?: ChannelType | '' }): string {
  if (ch.provider) return ch.provider;
  return channelTypeOf(ch.channelType)?.label ?? '非厂商渠道';
}

/** 渠道徽标的备选字形(provider 为空时用类型字形,避免 ProviderMark 渲染空白)。 */
export function channelMark(ch: { provider: Provider; channelType?: ChannelType | '' }): string {
  return ch.provider || channelTypeOf(ch.channelType)?.mark || '—';
}
