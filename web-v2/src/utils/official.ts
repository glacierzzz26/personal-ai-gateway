import type { ChannelType, ModelCatalogItem, ModelOffer, OfficialPriceView, PriceSource, Provider } from '@/types';

/** 官方价索引。同一 (厂商, 模型) 可有两来源并存(CC + opencode,见后端 m0014),故双表:
 *  - byVM:`${provider}|${modelName}` —— 兼容旧查找;同键多来源时保留 commandcode(canonical 锚点);
 *  - byVMS:`${provider}|${modelName}|${source}` —— 精确到来源(按 offer 渠道类型取对应来源)。 */
export interface OfficialIndex {
  byVM: Map<string, OfficialPriceView>;
  byVMS: Map<string, OfficialPriceView>;
}

/** 来源归一:空串 = 迁移前遗留,读作 commandcode(与后端 domain.PriceSource 一致)。 */
function normSource(s?: PriceSource): 'commandcode' | 'opencode' {
  return s === 'opencode' ? 'opencode' : 'commandcode';
}

export function buildOfficialIndex(rows: OfficialPriceView[]): OfficialIndex {
  const byVM = new Map<string, OfficialPriceView>();
  const byVMS = new Map<string, OfficialPriceView>();
  for (const op of rows) {
    const vm = `${op.provider}|${op.modelName}`;
    const src = normSource(op.source);
    byVMS.set(`${vm}|${src}`, op);
    // 同键多来源:commandcode 优先(CC 是主力/兜底锚点);无既有行则直接落。
    const prev = byVM.get(vm);
    if (!prev || src === 'commandcode') byVM.set(vm, op);
  }
  return { byVM, byVMS };
}

/** 供给源应取的来源锚点(镜像后端 domain.PriceSourceForChannelType):opencode 渠道取 OC,其余(含空/未知)取 CC。 */
export function priceSourceForChannelType(t?: ChannelType): 'commandcode' | 'opencode' {
  return t === 'opencode' ? 'opencode' : 'commandcode';
}

/** 单键查找:先按来源精确命中,再回落到来源无关的旧索引(展示降级)。 */
function lookup(
  index: OfficialIndex,
  provider: Provider,
  name: string | undefined,
  src: 'commandcode' | 'opencode',
): OfficialPriceView | undefined {
  if (!name) return undefined;
  return index.byVMS.get(`${provider}|${name}|${src}`) ?? index.byVM.get(`${provider}|${name}`);
}

/** 末段 '/' 之后,去空白 + 小写(镜像后端 store.canonicalModelKey)。 */
export function canonicalName(s?: string): string {
  const t = (s ?? '').trim();
  const i = t.lastIndexOf('/');
  return (i >= 0 ? t.slice(i + 1) : t).trim().toLowerCase();
}

/** 模型级显式绑定命中的官方价行(聚合渠道模型名与官方页名对不上时的兜底)。
 *  模型级无渠道上下文 → 取来源无关口径(commandcode 优先),与旧行为一致。 */
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
 * 来源维度:优先按 offer 所属渠道类型解析出的来源(CC / opencode)取行,取不到再回落到来源无关口径 ——
 * 双来源并存时(opencode 渠道的 Claude 走 OC 价,CC 渠道走 CC 价)各自命中正确的行。
 */
export function officialFor(
  index: OfficialIndex,
  m: ModelCatalogItem,
  o: ModelOffer,
): OfficialPriceView | undefined {
  const src = priceSourceForChannelType(o.channelType);

  const bound = modelBinding(index, m);
  if (bound) return bound;

  const direct =
    lookup(index, o.provider, o.upstreamModel, src) ??
    lookup(index, o.provider, m.originalName, src) ??
    lookup(index, o.provider, m.name, src);
  if (direct) return direct;

  const v = o.inferredVendor ?? m.inferredVendor;
  if (!v) return undefined;
  return (
    lookup(index, v, o.upstreamModel, src) ??
    lookup(index, v, m.originalName, src) ??
    lookup(index, v, m.name, src) ??
    lookup(index, v, canonicalName(o.upstreamModel ?? m.originalName), src)
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
