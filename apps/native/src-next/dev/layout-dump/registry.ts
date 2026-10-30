/** Curated DEV observations, never a raw props/Fiber/state inspection surface. */
import type {HostRect} from './native-host.ts';
export type HostKind = 'view' | 'text' | 'input' | 'image';
export type ScalarStyle = Readonly<Record<string, string | number>>;
export type HostFacts = Readonly<{
  kind: HostKind;
  semanticId?: string;
  role?: string;
  /** Final theme-resolved StyleSheet.flatten output from this owning primitive. */
  flattenedStyle?: unknown;
  /** True only for the complete currently evaluated style, never a partial recipe. */
  declaredStyleComplete?: boolean;
}>;
export type ResolvedStyle = Readonly<{
  /** The supplier must observe/resolve these on the native host, not infer them from input style. */
  fontFamily?: string; fontSize?: number; fontWeight?: number; color?: string;
  /** Active shaped resource's verified typographic name16, else name1. */
  resolvedFontFamily?: string;
  backgroundColor?: string; effectiveBackgroundColor?: string; effectiveOpacity?: number;
}>;
export type NativeTextLine = Readonly<{
  x: number; y: number; width: number; height: number; text: string;
  ascender?: number; capHeight?: number; descender?: number; xHeight?: number;
}>;
export type HostObservation = Readonly<{
  kind: HostKind; semanticId?: string; role?: string;
  flattenedStyle: ScalarStyle;
  declaredStyleComplete: boolean;
  measuredStyle: ScalarStyle;
  effectiveOpacity?: number;
  effectiveBackgroundColor?: string;
  lines?: readonly NativeTextLine[];
  maskedLength?: number;
}>;
export type HostRegistration = Readonly<{
  update(facts: HostFacts): void;
  recordResolvedStyle(facts: ResolvedStyle): void;
  recordTextLayout(lines: unknown): void;
  /** Actual current window-space native runs; never a Text host-box fallback. */
  observeTextRectangles(reader?: () => Promise<unknown>): void;
  invalidateTextLayout(): void;
  invalidateResolvedStyle(): void;
  recordNativeFocus(focused: boolean): void;
  recordMaskLength(length: number): void;
  unregister(): void;
}>;

// This producer supports these native scalar facts. The shared contract remains
// the authority; compatibility tests import its public allowlist without copying it.
const scalarInputs = [
  'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'lineHeight', 'letterSpacing', 'textAlign', 'textTransform',
  'color', 'backgroundColor', 'opacity', 'borderRadius', 'borderWidth', 'borderColor',
  'shadowColor', 'shadowOpacity', 'shadowRadius', 'shadowOffsetX', 'shadowOffsetY',
  'textDecorationLine', 'textShadowColor', 'textShadowRadius', 'textShadowOffsetX', 'textShadowOffsetY',
  'elevation', 'overflow', 'display', 'position', 'zIndex',
  'width', 'height', 'minWidth', 'minHeight', 'maxWidth', 'maxHeight', 'top', 'right', 'bottom', 'left',
  'padding', 'paddingHorizontal', 'paddingVertical', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
  'margin', 'marginHorizontal', 'marginVertical', 'marginTop', 'marginRight', 'marginBottom', 'marginLeft',
  'gap', 'rowGap', 'columnGap', 'alignItems', 'alignSelf', 'justifyContent', 'flexDirection', 'flexGrow', 'flexShrink', 'flexBasis',
] as const;
const fontAndLayoutInputs = new Set<string>([
  'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'lineHeight', 'letterSpacing', 'textTransform',
  'width', 'height', 'maxWidth', 'maxHeight', 'padding', 'paddingHorizontal', 'paddingVertical',
  'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
]);
const object = (value: unknown): value is object => typeof value === 'object' && value !== null;
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const safeText = (value: unknown, maximum = 256): value is string => typeof value === 'string' && value.length > 0
  && value.length <= maximum && !/[\x00-\x1f\x7f]/.test(value);
const assetLike = /url\s*\(|(?:https?|file|data|content|asset|blob):|[\\/]|\.(?:png|jpe?g|webp|svg|gif|ttf|otf|woff2?)(?:[?#]|$)/i;
function data(value: object, key: string): unknown {
  const property = Object.getOwnPropertyDescriptor(value, key);
  return property && 'value' in property ? property.value : undefined;
}

export function curateFlattenedStyle(value: unknown): ScalarStyle {
  const out: Record<string, string | number> = {};
  if (!object(value) || Array.isArray(value)) return Object.freeze(out);
  for (const key of scalarInputs) {
    const fact = data(value, key);
    if (finite(fact) || safeText(fact, 512) && !assetLike.test(fact)) out[key] = fact;
  }
  for (const key of ['shadowOffset', 'textShadowOffset'] as const) {
    const offset = data(value, key);
    if (!object(offset) || Array.isArray(offset)) continue;
    const x = data(offset, 'width'), y = data(offset, 'height');
    if (finite(x) && finite(y)) {out[`${key}X`] = x; out[`${key}Y`] = y;}
  }
  return Object.freeze(out);
}

function completeDeclaredInput(raw: unknown, curated: ScalarStyle): boolean {
  if (!object(raw) || Array.isArray(raw)) return false;
  // A dropped unsafe/getter value must not become an omitted RN default later.
  return scalarInputs.every(key => {
    const property = Object.getOwnPropertyDescriptor(raw, key);
    return !property || 'value' in property && (property.value === undefined || key in curated);
  }) && (['shadowOffset', 'textShadowOffset'] as const).every(key => {
    const property = Object.getOwnPropertyDescriptor(raw, key);
    return !property || 'value' in property && (property.value === undefined || `${key}X` in curated && `${key}Y` in curated);
  });
}

function textLines(value: unknown): readonly NativeTextLine[] | undefined {
  if (!Array.isArray(value) || value.length > 256) return undefined;
  const lines: NativeTextLine[] = [];
  let characters = 0;
  for (const raw of value) {
    if (!object(raw)) return undefined;
    const x = data(raw, 'x'), y = data(raw, 'y'), width = data(raw, 'width'), height = data(raw, 'height'), text = data(raw, 'text');
    if (!finite(x) || !finite(y) || !finite(width) || !finite(height) || width < 0 || height < 0
        || typeof text !== 'string' || text.length > 8192) return undefined;
    characters += text.length;
    if (characters > 65536) return undefined;
    const line: {x: number; y: number; width: number; height: number; text: string; ascender?: number; capHeight?: number; descender?: number; xHeight?: number} = {x, y, width, height, text};
    for (const key of ['ascender', 'capHeight', 'descender', 'xHeight'] as const) {
      const fact = data(raw, key);
      if (finite(fact)) line[key] = fact;
    }
    lines.push(Object.freeze(line));
  }
  return Object.freeze(lines);
}

function observedStyle(raw: ResolvedStyle): Pick<HostObservation, 'measuredStyle' | 'effectiveOpacity' | 'effectiveBackgroundColor'> {
  const style: Record<string, string | number> = {};
  if (safeText(raw.fontFamily) && !assetLike.test(raw.fontFamily)) style.platformFontFamily = raw.fontFamily;
  if (safeText(raw.resolvedFontFamily) && !assetLike.test(raw.resolvedFontFamily)) style.resolvedFontFamily = raw.resolvedFontFamily;
  if (finite(raw.fontSize) && raw.fontSize > 0) style.fontSize = `${raw.fontSize}px`;
  if (finite(raw.fontWeight) && Number.isInteger(raw.fontWeight) && raw.fontWeight >= 1 && raw.fontWeight <= 1000) style.fontWeight = String(raw.fontWeight);
  for (const key of ['color', 'backgroundColor'] as const) {
    const color = raw[key];
    if (safeText(color) && /^rgba?\([\d., %]+\)$/.test(color)) style[key] = color;
  }
  const opacity = finite(raw.effectiveOpacity) && raw.effectiveOpacity >= 0 && raw.effectiveOpacity <= 1 ? raw.effectiveOpacity : undefined;
  const background = safeText(raw.effectiveBackgroundColor) && /^rgba?\([\d., %]+\)$/.test(raw.effectiveBackgroundColor) ? raw.effectiveBackgroundColor : undefined;
  return {measuredStyle: Object.freeze(style), effectiveOpacity: opacity, effectiveBackgroundColor: background};
}

function metadata(facts: HostFacts): HostObservation {
  const flattenedStyle = curateFlattenedStyle(facts.flattenedStyle);
  return Object.freeze({kind: facts.kind,
    ...(safeText(facts.semanticId) ? {semanticId: facts.semanticId} : {}),
    ...(safeText(facts.role, 80) ? {role: facts.role} : {}),
    flattenedStyle,
    declaredStyleComplete: facts.declaredStyleComplete === true && completeDeclaredInput(facts.flattenedStyle, flattenedStyle),
    measuredStyle: Object.freeze({})});
}

/** Registration leases fence ref replacement, unmount and capture-epoch retirement. */
export function createNativeMeasurementRegistry() {
  type Record = {host: object; epoch: number; observation: HostObservation; textRectangles?: () => Promise<unknown>};
  let epoch = 1;
  let revision = 0;
  let records = new WeakMap<object, Record>();
  let focused: Record | undefined;
  const current = (record: Record) => record.epoch === epoch && records.get(record.host) === record;
  return {
    register(host: object, facts: HostFacts): HostRegistration {
      if (focused?.host === host) focused = undefined;
      const record: Record = {host, epoch, observation: metadata(facts)};
      records.set(host, record);
      revision++;
      const replace = (patch: Partial<HostObservation>) => {record.observation = Object.freeze({...record.observation, ...patch}); revision++;};
      return Object.freeze({
        update(next: HostFacts) {
          if (!current(record) || next.kind !== record.observation.kind) return;
          const before = record.observation, after = metadata(next);
          const textChanged = [...fontAndLayoutInputs].some(key => before.flattenedStyle[key] !== after.flattenedStyle[key]);
          const paintChanged = JSON.stringify(before.flattenedStyle) !== JSON.stringify(after.flattenedStyle);
          record.observation = Object.freeze({...after,
            ...(textChanged ? {} : {lines: before.lines}),
            ...(paintChanged ? {} : {measuredStyle: before.measuredStyle, effectiveOpacity: before.effectiveOpacity, effectiveBackgroundColor: before.effectiveBackgroundColor}),
            maskedLength: before.maskedLength});
          revision++;
        },
        recordResolvedStyle(facts: ResolvedStyle) {if (current(record)) replace(observedStyle(facts));},
        recordTextLayout(lines: unknown) {
          if (!current(record) || record.observation.kind !== 'text') return;
          replace({lines: textLines(lines)});
        },
        observeTextRectangles(reader?: () => Promise<unknown>) {
          if (!current(record) || record.observation.kind !== 'text') return;
          record.textRectangles = reader;
          revision++;
        },
        invalidateTextLayout() {if (current(record)) replace({lines: undefined});},
        invalidateResolvedStyle() {
          if (current(record)) replace({measuredStyle: Object.freeze({}), effectiveOpacity: undefined, effectiveBackgroundColor: undefined});
        },
        recordNativeFocus(isFocused: boolean) {
          if (!current(record) || typeof isFocused !== 'boolean') return;
          if (isFocused) focused = record;
          else if (focused === record) focused = undefined;
          revision++;
        },
        recordMaskLength(length: number) {
          if (current(record) && record.observation.kind === 'input' && Number.isSafeInteger(length) && length >= 0 && length <= 8192) replace({maskedLength: length});
        },
        unregister() {
          if (!current(record)) return;
          records.delete(host);
          if (focused === record) focused = undefined;
          revision++;
        },
      });
    },
    read(host: object): HostObservation | undefined {return records.get(host)?.observation;},
    async measureTextRectangles(host: object, signal?: AbortSignal): Promise<readonly HostRect[] | undefined> {
      const record = records.get(host), reader = record?.textRectangles;
      if (!record || !current(record) || record.observation.kind !== 'text' || !reader || signal?.aborted) return undefined;
      let abort: (() => void) | undefined;
      try {
        const pending = Promise.resolve().then(() => current(record) && record.textRectangles === reader && !signal?.aborted ? reader() : undefined);
        const stopped = new Promise<undefined>(resolve => {
          abort = () => resolve(undefined);
          signal?.addEventListener('abort', abort, {once: true});
          if (signal?.aborted) abort();
        });
        const raw = signal ? await Promise.race([pending, stopped]) : await pending;
        if (!current(record) || record.textRectangles !== reader || signal?.aborted || !Array.isArray(raw) || raw.length > 256) return undefined;
        const rects: HostRect[] = [];
        for (const line of raw) {
          if (!object(line)) return undefined;
          const x = data(line, 'x'), y = data(line, 'y'), width = data(line, 'width'), height = data(line, 'height');
          if (!finite(x) || !finite(y) || !finite(width) || !finite(height) || width < 0 || height < 0) return undefined;
          rects.push(Object.freeze({x, y, width, height}));
        }
        return Object.freeze(rects);
      } catch {return undefined;}
      finally {if (abort) signal?.removeEventListener('abort', abort);}
    },
    actualFocus(host: object, mounted: ReadonlySet<object>): boolean | undefined {
      if (!focused || !current(focused) || !mounted.has(focused.host)) return undefined;
      return focused.host === host;
    },
    revision() {return revision;},
    retireEpoch() {epoch++; revision++; records = new WeakMap(); focused = undefined;},
  };
}

export type NativeMeasurementRegistry = ReturnType<typeof createNativeMeasurementRegistry>;
