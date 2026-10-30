/** Produces the public fidelity contract; validation/comparison stay in server tools/fidelity. */
import {measureNativeHost, type HostRect, type NativeHostReader} from './native-host.ts';
import type {NativeMeasurementRegistry} from './registry.ts';
import type {NativeObservedHost, NativeTreeObserver} from './native-observer.ts';
import {createNativeWindowGeometry} from './native-window-geometry.ts';

export type NativeViewport = Readonly<{width: number; height: number; deviceScaleFactor: number}>;
export type NativeScreenDiagnostics = Readonly<{fontScale?: number}>;
export type NativeCaptureOptions = Readonly<{
  root: object;
  viewport: NativeViewport;
  platform: 'android' | 'ios' | 'tvos';
  registry: NativeMeasurementRegistry;
  /** Actual platform metadata only; never used for measurement arithmetic. */
  diagnostics?: NativeScreenDiagnostics;
  observe?: NativeTreeObserver;
  isCurrent?: () => boolean;
  /** Explicit public semantic ID to frame anchor; unlisted IDs are not serialized. */
  anchors?: Readonly<Record<string, string>>;
  signal?: AbortSignal;
}>;
type Node = object & NativeHostReader & {
  nodeType?: unknown; nodeName?: unknown; childNodes?: unknown; isConnected?: unknown;
  offsetWidth?: unknown; offsetHeight?: unknown;
};
type Element = Record<string, unknown> & {id: string; parent: string | null; box: HostRect | null};
const normalize = (value: string) => value.replace(/\s+/g, ' ').trim();
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
// This producer only emits RN dp/pt. Match the canonical sameLogicalExtent
// representational relation; do not round observations or use a geometric epsilon.
const sameNativeExtent = (left: number, right: number) => finite(left) && finite(right)
  && (left === right || left === Math.fround(right) || right === Math.fround(left));

export async function collectNativeScreen(options: NativeCaptureOptions) {
  const {registry, root, viewport, signal} = options;
  const elements: Element[] = [], strings: Record<string, unknown>[] = [];
  const unavailable: {element: string; property: string; reason: string}[] = [];
  const missingKeys = new Set<string>();
  const anchorIssues: {kind: string; frame: string; elements: string[]}[] = [];
  const missing = (element: string, property: string, reason: string) => {
    const key = `${element}\0${property}\0${reason}`;
    if (!missingKeys.has(key)) {missingKeys.add(key); unavailable.push({element, property, reason});}
  };
  const revision = registry.revision();
  const measuredRoot = await measureNativeHost(root, {signal});
  if (signal?.aborted) throw new Error('Native layout capture was cancelled.');
  if ((root as Node).isConnected === false) missing('root', 'isConnected', 'native root is no longer mounted');
  const mounted = new Set<object>();
  const nodes = new Map<string, Node>();
  const childRecords = new Map<string, Element[]>();
  const anchors = new Map<string, string[]>();
  const childrenByHost = new Map<object, object[]>(), childErrors = new Map<object, string>();
  const discovered: object[] = [], seen = new Set<object>(), pending: Node[] = [root];
  while (pending.length && discovered.length < 2000) {
    if (signal?.aborted) break;
    const node = pending.pop()!;
    if (seen.has(node)) {missing('root', 'elements', 'native tree repeats a host'); continue;}
    seen.add(node);
    if (node.isConnected === false || node.nodeType === 3) continue;
    discovered.push(node);
    const observed = registry.read(node), tag = typeof node.nodeName === 'string' ? node.nodeName : '';
    const opaque = /TextInput|EditText|UITextField|SecureText|Image|SVG/i.test(tag)
      || observed?.kind === 'input' || observed?.kind === 'image';
    const children: object[] = [];
    if (!opaque) {
      try {
        const value = node.childNodes;
        if (value && typeof value === 'object' && Symbol.iterator in value) {
          for (const child of value as Iterable<unknown>) {
            if (children.length >= 2000) {childErrors.set(node, 'native child list exceeds capture bound'); break;}
            if (typeof child === 'object' && child !== null && (child as Node).nodeType !== 3) children.push(child);
          }
        } else childErrors.set(node, 'public native childNodes traversal is unavailable');
      } catch {childErrors.set(node, 'public native childNodes traversal failed');}
    }
    childrenByHost.set(node, children);
    for (let index = children.length - 1; index >= 0; index--) pending.push(children[index]);
  }
  if (pending.length) missing('root', 'elements', 'native tree exceeds capture bound');
  if (!options.observe) throw new Error('A scoped native-window observer is required; public layout is diagnostic only.');
  if (!options.isCurrent) throw new Error('A current public fixture guard is required for native observation.');
  const fixtures = discovered.filter(host => {
    const facts = registry.read(host);
    return facts?.kind === 'view' && ['accounts-welcome', 'tv-front-door'].includes(facts.semanticId ?? '');
  });
  if (fixtures.length !== 1) throw new Error('A unique registered public fixture root is required.');
  const fixture = fixtures[0], semanticId = registry.read(fixture)!.semanticId;
  const scopeCurrent = () => !signal?.aborted && options.isCurrent!() && registry.read(fixture)?.semanticId === semanticId
    && (root as Node).isConnected !== false && (fixture as Node).isConnected !== false;
  if (!scopeCurrent()) throw new Error('The public native fixture retired before observation.');
  const native: ReadonlyMap<object, NativeObservedHost> = await options.observe({root, fixture, hosts: discovered, signal, isCurrent: scopeCurrent});
  if (!scopeCurrent()) throw new Error('The public native fixture retired during observation.');
  const geometry = createNativeWindowGeometry(root, native, scopeCurrent);
  const nativeRoot = geometry.root();
  if (!nativeRoot) throw new Error('Current scoped native-window root bounds are unavailable.');
  const rootBox = nativeRoot.windowRect;
  const readback = await measureNativeHost({measureInWindow: (root as Node).measureInWindow?.bind(root)}, {signal});
  if (!scopeCurrent()) throw new Error('The public native fixture retired during coordinate readback.');
  const knownViewport = finite(viewport.width) && viewport.width > 0 && finite(viewport.height) && viewport.height > 0
    && finite(viewport.deviceScaleFactor) && viewport.deviceScaleFactor > 0;
  const fullViewport = knownViewport && rootBox.x === 0 && rootBox.y === 0 && rootBox.width > 0 && rootBox.height > 0
    && sameNativeExtent(rootBox.width, viewport.width) && sameNativeExtent(rootBox.height, viewport.height);
  if (!fullViewport) missing('root', 'fullViewport',
    `API=${nativeRoot.source}; coordinate-space=attached-native-window; raw=${JSON.stringify(rootBox)}; native origin or viewport extent does not match`);
  const walk = async (node: Node, parent: string | null, id: string): Promise<void> => {
    if (signal?.aborted) {missing('root', 'captureState', 'capture cancelled'); return;}
    if (elements.length >= 2000) {missing('root', 'elements', 'native tree exceeds capture bound'); return;}
    if (node.isConnected === false || node.nodeType === 3) return;
    if (mounted.has(node)) return;
    mounted.add(node);
    nodes.set(id, node);
    const observed = registry.read(node);
    const snapshot = native.get(node);
    const unavailableReason = (property: string, fallback: string) => snapshot?.unavailable[property]
      ?? snapshot?.unavailable.nativeObservation ?? fallback;
    const tag = typeof node.nodeName === 'string' && /^[A-Za-z][A-Za-z0-9:_-]{0,100}$/.test(node.nodeName) ? node.nodeName : 'unknown';
    if (tag === 'unknown') missing(id, 'tag', 'public native node name is unavailable');
    const input = /TextInput|EditText|UITextField|SecureText/i.test(tag) || observed?.kind === 'input';
    const image = !input && (/Image|SVG/i.test(tag) || observed?.kind === 'image');
    const text = !input && (observed?.kind === 'text' || /Text|Paragraph/i.test(tag));
    const measured = node === root ? measuredRoot : await measureNativeHost(node, {signal});
    if (!scopeCurrent()) throw new Error('The public native fixture retired while hosts were measured.');
    const projected = geometry.host(node), box = projected.box;
    if (!box) missing(id, 'box', unavailableReason('windowRect', projected.unavailable.box ?? 'native host window bounds unavailable'));
    const children = childrenByHost.get(node) ?? [];
    if (childErrors.has(node)) missing(id, 'children', childErrors.get(node)!);
    const elementChildren = children.filter(child => (child as Node).nodeType !== 3);
    const mapped = observed?.semanticId && options.anchors ? Object.getOwnPropertyDescriptor(options.anchors, observed.semanticId)?.value : undefined;
    const frame = typeof mapped === 'string' && /^[A-Za-z0-9._:-]{1,256}$/.test(mapped) ? mapped : undefined;
    if (frame) anchors.set(frame, [...(anchors.get(frame) ?? []), id]);
    const opacity = snapshot?.effectiveOpacity;
    const background = snapshot?.effectiveBackgroundColor;
    const diagnostics: Record<string, unknown> = {flattenedStyle: observed?.flattenedStyle ?? {},
      ...(observed?.declaredStyleComplete ? {declaredStyle: {source: 'theme-resolved-stylesheet', complete: true}} : {}),
      ...(projected.windowRect ? {nativeWindowRect: projected.windowRect} : {}),
      ...('rect' in measured ? {publicMeasurement: {api: measured.source, rect: measured.rect}} : {})};
    const record: Element = {id, parent, frame: frame ?? null, tag, role: observed?.role ?? null,
      leaf: elementChildren.length === 0, box: box ?? null, style: snapshot?.style ?? {}, focused: null,
      effectiveOpacity: opacity ?? null,
      effectiveBackgroundColor: background,
      diagnostics};
    if (record.effectiveOpacity === null) missing(id, 'effectiveOpacity', unavailableReason('effectiveOpacity', 'current native paint opacity has not been observed'));
    if (!record.effectiveBackgroundColor) missing(id, 'effectiveBackgroundColor', unavailableReason('effectiveBackgroundColor', 'ancestor-composited native background has not been observed'));
    for (const property of ['backgroundColor', 'borderRadius', 'boxShadow']) {
      if (typeof snapshot?.style[property] !== 'string') missing(id, property, unavailableReason(property, 'native paint property is unavailable'));
    }
    const visiblePaint = box && rootBox && box.width > 0 && box.height > 0 && box.x < rootBox.width && box.y < rootBox.height
      && box.x + box.width > 0 && box.y + box.height > 0 && opacity !== undefined && opacity > 0;
    if (!input && text) {
      if (!visiblePaint) {
        missing(id, 'ownText', 'visible native paint has not been observed; hidden/unverified text is not exported');
        missing(id, 'textRects', 'visible native Text paint has not been observed');
      } else if (!observed?.lines || elementChildren.length) {
        missing(id, 'ownText', 'native Text layout/leaf ownership is not available');
        missing(id, 'textRects', 'actual native Text line geometry is not available');
      } else {
        record.ownText = observed.lines.map(line => line.text).join('');
        // Only this admitted native batch supplies geometry, including animated transforms.
        if (projected.textRects?.length && projected.textRects.length === observed.lines.length) {
          record.textRects = projected.textRects;
          diagnostics.nativeWindowTextRects = projected.windowTextRects;
        } else missing(id, 'textRects', unavailableReason('textLineRects', 'current scoped native window text runs are not observed; host boxes are not text runs'));
        if (record.ownText) strings.push({id: `${id}:text:0`, owner: id, frame: frame ?? null, kind: 'text', text: record.ownText, rects: record.textRects, style: record.style});
      }
      for (const property of ['resolvedFontFamily', 'fontSize', 'fontWeight', 'color', 'fontStyle', 'letterSpacing',
        'textTransform', 'textDecorationLine', 'textShadow']) {
        if (typeof (record.style as Record<string, unknown>)[property] !== 'string') missing(id, property,
          unavailableReason(property === 'color' ? 'textColor' : property, 'resolved native font/paint fact is not available'));
      }
    } else record.ownText = '';
    if (input) {
      record.input = {value: null, redacted: true, maskedLength: observed?.maskedLength};
      if (observed?.maskedLength === undefined) missing(id, 'maskedLength', 'rendered mask length is not observed');
    }
    if (image) {
      record.image = {kind: /SVG/i.test(tag) ? 'icon' : 'image'};
      missing(id, 'image.fit', 'current native image fit has not been observed');
      missing(id, 'image.loaded', 'current native image load state has not been observed');
    }
    elements.push(record);
    if (parent !== null) childRecords.set(parent, [...(childRecords.get(parent) ?? []), record]);
    for (let index = 0; index < elementChildren.length; index++) {
      if (elements.length >= 2000) {missing('root', 'elements', 'native tree exceeds capture bound'); break;}
      await walk(elementChildren[index], id, `${id}.${index}`);
    }
  };
  await walk(root, null, 'root');
  for (const element of elements) {
    const node = nodes.get(element.id)!;
    const snapshot = native.get(node);
    const focused = snapshot?.focused;
    element.focused = focused ?? null;
    if (focused === undefined) missing(element.id, 'focused', snapshot?.unavailable.isFocused
      ?? snapshot?.unavailable.nativeObservation ?? 'current scoped native focus fact is unavailable');
  }
  for (const element of [...elements].reverse()) {
    const children = childRecords.get(element.id) ?? [];
    if (typeof element.ownText === 'string' && children.every(child => typeof child.normalizedText === 'string')) {
      element.normalizedText = normalize([element.ownText, ...children.map(child => child.normalizedText)].join(' '));
    } else missing(element.id, 'normalizedText', 'visible descendant native Text is unavailable');
    const knownOwnRects = element.ownText === '' || typeof element.ownText === 'string' && Array.isArray(element.textRects);
    if (knownOwnRects && children.every(child => Array.isArray(child.descendantTextRects))) {
      element.descendantTextRects = [
        ...(Array.isArray(element.textRects) ? element.textRects : []),
        ...children.flatMap(child => child.descendantTextRects as unknown[]),
      ];
    } else missing(element.id, 'descendantTextRects', 'some native descendant text geometry is unavailable');
  }
  for (const [frame, ids] of anchors) if (ids.length > 1) anchorIssues.push({kind: 'duplicate', frame, elements: ids});
  if (registry.revision() !== revision) missing('root', 'captureState', 'native observations changed while the tree was measured');
  if (!scopeCurrent()) throw new Error('The public native fixture retired before publication.');
  const fontScale = options.diagnostics?.fontScale;
  return {version: 1, scope: 'screen', measurement: {renderer: 'react-native', platform: options.platform,
    coordinateUnit: options.platform === 'android' ? 'dp' : 'pt', geometrySource: 'native-window'},
    diagnostics: {...(finite(fontScale) && fontScale > 0 ? {fontScale} : {}),
      nativeCoordinates: {basis: 'attached-native-window', windowAPI: nativeRoot.source, rootWindow: rootBox,
        ...('rect' in measuredRoot ? {publicRoot: {api: measuredRoot.source, rect: measuredRoot.rect}} : {}),
        ...('rect' in readback ? {publicRootReadback: {api: readback.source, rect: readback.rect}} : {})}},
    root: {x: 0, y: 0, width: rootBox.width, height: rootBox.height},
    viewport: {...viewport}, fullViewport, elements, strings, anchorIssues, repeatedLists: [], unavailable};
}
