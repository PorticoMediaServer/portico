import {validateMeasurement} from './contract.mjs';

/** Reference-only selectors. Never import into an application or native payload. */
export const frontdoorReferences = {
  phone: {
    selector: '.frame[data-f="39"] > .screen > .col:nth-of-type(1) .phone',
    windowViewport: {width: 1440, height: 1010, deviceScaleFactor: 1},
    viewport: {width: 390, height: 844, deviceScaleFactor: 1},
    anchors: {backdrop: ':scope > .gen.top', 'brand-mark': 'img.mk', title: 'h1', 'primary-action': '.btn.play', 'secondary-action': '.btn.glass'},
  },
  androidTV: {
    selector: '.frame[data-f="40"] > .screen.tv',
    windowViewport: {width: 1920, height: 1080, deviceScaleFactor: 1},
    viewport: {width: 1920, height: 1080, deviceScaleFactor: 1},
    anchors: {screen: '$root', backdrop: ':scope > .gen', 'brand-mark': 'img.wm', title: 'h1', 'primary-action': '.btns .btn.play', 'secondary-action': '.btns .btn.glass'},
  },
};

/** App-side public IDs; private selectors/IDs stay exclusively in this tool. */
export const frontdoorStateAdapters = {
  phone: {anchors: {'colour-backdrop': 'backdrop', 'accounts-welcome-wordmark': 'brand-mark', 'accounts-welcome-title': 'title', 'accounts-welcome-portico': 'primary-action', 'accounts-welcome-portico/label': 'primary-action-label',
    'accounts-welcome-local': 'secondary-action', 'accounts-welcome-local/label': 'secondary-action-label'},
    comparison: {textHandledBySlots: ['primary-action', 'secondary-action'], actualContentOnly: ['primary-action-label', 'secondary-action-label'],
      contentSlots: ['primary', 'secondary'].map(name => ({reference: name + '-action', actual: name + '-action-label'}))}},
  androidTV: {anchors: {'tv-front-door': 'screen', 'colour-backdrop': 'backdrop', 'tv-front-door-wordmark': 'brand-mark', 'tv-front-door-title': 'title', 'tv-front-door-portico': 'primary-action', 'tv-front-door-portico/label': 'primary-action-label',
    'tv-front-door-local': 'secondary-action', 'tv-front-door-local/label': 'secondary-action-label'},
    comparison: {focus: {frame: 'primary-action'}, textHandledBySlots: ['primary-action', 'secondary-action'], actualContentOnly: ['primary-action-label', 'secondary-action-label'],
      contentSlots: ['primary', 'secondary'].map(name => ({reference: name + '-action', actual: name + '-action-label'}))}},
};

/** Current declared-paint cohort: the same existing Button fill hosts now have
 * public DEV IDs. Keep historical recipes unchanged for historical captures. */
export function frontdoorDeclaredStateAdapter(platform) {
  const old = frontdoorStateAdapters[platform];
  if (!old) throw new Error('Unknown public frontdoor platform');
  const adapter = structuredClone(old);
  const prefix = platform === 'phone' ? 'accounts-welcome' : 'tv-front-door';
  for (const [id, name] of [['portico', 'primary'], ['local', 'secondary']]) adapter.anchors[`${prefix}-${id}/face`] = `${name}-action-face`;
  adapter.comparison.actualContentOnly.push('primary-action-face', 'secondary-action-face');
  adapter.comparison.paintSlots = ['primary', 'secondary'].map(name => ({reference: name + '-action', actual: name + '-action-face', properties: ['effectiveBackgroundColor']}));
  return adapter;
}

/** Add verified matching annotations to a cached genuine reference. Preserve
 * all observed bounds/style/text/density and keep the input file immutable. */
export function annotateFrontdoorReference(measured, platform) {
  const spec = frontdoorReferences[platform];
  if (!spec || measured?.measurement?.sourceRole !== 'reference-frame' || validateMeasurement(measured, {reference: true}).length ||
    measured.anchorIssues.length || measured.viewport.width !== spec.viewport.width || measured.viewport.height !== spec.viewport.height)
    throw new Error('A complete validated cached frontdoor reference at its authored size is required');
  const result = structuredClone(measured), bound = new Map(), roots = result.elements.filter(node => node.parent == null);
  if (roots.length !== 1) throw new Error('Cached frontdoor root must be unique');
  const predicates = {backdrop: node => node.parent === roots[0].id && node.tag === 'div' && node.classes?.includes('gen') && (platform === 'androidTV' || node.classes.includes('top')),
    'brand-mark': node => node.tag === 'img' && node.image?.kind === 'image' && node.classes?.includes(platform === 'phone' ? 'mk' : 'wm'),
    title: node => node.tag === 'h1',
    'primary-action': node => node.classes?.includes('btn') && node.classes.includes('play'),
    'secondary-action': node => node.classes?.includes('btn') && node.classes.includes('glass')};
  if (platform === 'androidTV') predicates.screen = node => node.parent == null && node.box?.x === 0 && node.box?.y === 0 &&
    node.box?.width === spec.viewport.width && node.box?.height === spec.viewport.height;
  for (const [frame, predicate] of Object.entries(predicates)) {
    const nodes = result.elements.filter(predicate);
    if (nodes.length !== 1 || nodes[0].frame && nodes[0].frame !== frame) throw new Error('Cached frontdoor anchor must be unique: ' + frame);
    nodes[0].frame = frame; bound.set(nodes[0].id, frame);
  }
  for (const node of result.elements) if (node.tag.startsWith('::') && bound.has(node.parent)) node.frame = bound.get(node.parent) + node.tag;
  for (const value of result.strings) if (bound.has(value.owner)) value.frame = bound.get(value.owner);
  return result;
}
