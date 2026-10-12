import type { ModelCatalogItem, ModelOffer, OfficialPriceView, Provider } from '@/types';

/** 官方价索引。2026-10 起官方价来源收敛为单一(commandcode),故只按 (厂商, 模型) 单键:
 *  `${provider}|${modelName}`。遗留空来源行读作 commandcode,与后端一致。 */
export interface OfficialIndex {
  byVM: Map<string, OfficialPriceView>;
}

export function buildOfficialIndex(rows: OfficialPriceView[]): OfficialIndex {
  const byVM = new Map<string, OfficialPriceView>();
  for (const op of rows) {
    const vm = `${op.provider}|${op.modelName}`;
    // 单来源后不该有重复键;万一有(遗留空来源 + commandcode),commandcode 优先落定。
    const prev = byVM.get(vm);
    if (!prev || op.source === 'commandcode') byVM.set(vm, op);
  }
  return { byVM };
}

/** 单键查找。 */
function lookup(
  index: OfficialIndex,
  provider: Provider,
  name: string | undefined,
): OfficialPriceView | undefined {
  if (!name) return undefined;
  return index.byVM.get(`${provider}|${name}`);
}

/** 末段 '/' 之后,去空白 + 小写(镜像后端 store.canonicalModelKey)。 */
export function canonicalName(s?: string): string {
  const t = (s ?? '').trim();
  const i = t.lastIndexOf('/');
  return (i >= 0 ? t.slice(i + 1) : t).trim().toLowerCase();
}

/** 模型级显式绑定命中的官方价行(聚合渠道模型名与官方页名对不上时的兜底)。 */
export function modelBinding(index: OfficialIndex, m: ModelCatalogItem): OfficialPriceView | undefined {
  if (!m.officialVendor || !m.officialModelName) return undefined;
  return index.byVM.get(`${m.officialVendor}|${m.officialModelName}`);
}

/**
 * 该供给源对应的官方参考价。查找序(严格,保证既有「provider 直连」行为不变):
 *   1. 模型级显式绑定(手工覆盖,优先级最高);
 *   2. provider 直连:offer.provider 即厂商时的现状三步回落;
 *   3. 推断厂商:聚合渠道(provider=OpenAI)按模型名推断出的厂商再试一轮。
 *
 * 官方价现为单一来源(commandcode),不再按渠道类型分来源。
 */
export function officialFor(
  index: OfficialIndex,
  m: ModelCatalogItem,
  o: ModelOffer,
): OfficialPriceView | undefined {
  const bound = modelBinding(index, m);
  if (bound) return bound;

  const direct =
    lookup(index, o.provider, o.upstreamModel) ??
    lookup(index, o.provider, m.originalName) ??
    lookup(index, o.provider, m.name);
  if (direct) return direct;

  const v = o.inferredVendor ?? m.inferredVendor;
  if (!v) return undefined;
  return (
    lookup(index, v, o.upstreamModel) ??
    lookup(index, v, m.originalName) ??
    lookup(index, v, m.name) ??
    lookup(index, v, canonicalName(o.upstreamModel ?? m.originalName))
  );
}

/** 模型维度:任一供给源命中即可(模型广场列表页展示「官方 ↗」角标)。 */
export function officialOfModel(
  index: OfficialIndex,
  m: ModelCatalogItem,
): OfficialPriceView | undefined {
  for (const o of m.offers) {
    const hit = officialFor(index, m, o);
    if (hit) return hit;
  }
  return modelBinding(index, m);
}
