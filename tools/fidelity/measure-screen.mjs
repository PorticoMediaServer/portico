/** Run inside the measured document. Returns measurements, never DOM/source/art bytes.
 * The caller records state and selects full-screen or explicit component scope. */
export async function measureScreen({selector, expectedViewport, anchors = {}, repeats = []} = {}) {
  const roots = document.querySelectorAll(selector);
  if (roots.length !== 1) throw new Error('Exactly one fidelity measurement root is required');
  const root = roots[0];
  await document.fonts.ready;
  await Promise.allSettled([...root.querySelectorAll('img')].map(image => image.decode()));
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  const bounds = root.getBoundingClientRect();
  if (!bounds.width || !bounds.height) throw new Error('Fidelity screen root has no layout');
  const round = number => Object.is(number, -0) ? 0 : number;
  const box = rect => ({x: round(rect.x - bounds.x), y: round(rect.y - bounds.y), width: round(rect.width), height: round(rect.height)});
  const properties = [
    'display', 'position', 'boxSizing', 'overflowX', 'overflowY', 'opacity', 'zIndex',
    'color', 'backgroundColor', 'backgroundImage', 'backgroundSize', 'backgroundPosition',
    'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'fontStretch', 'fontFeatureSettings',
    'fontVariationSettings', 'lineHeight', 'letterSpacing', 'textAlign', 'textTransform',
    'whiteSpace', 'textDecorationLine', 'textDecorationColor', 'textShadow',
    'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
    'borderTopColor', 'borderRightColor', 'borderBottomColor', 'borderLeftColor',
    'borderTopStyle', 'borderRightStyle', 'borderBottomStyle', 'borderLeftStyle',
    'borderTopLeftRadius', 'borderTopRightRadius', 'borderBottomRightRadius', 'borderBottomLeftRadius',
    'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
    'marginTop', 'marginRight', 'marginBottom', 'marginLeft', 'gap', 'rowGap', 'columnGap',
    'alignItems', 'justifyContent', 'flexDirection', 'flexGrow', 'flexShrink', 'flexBasis',
    'boxShadow', 'filter', 'backdropFilter', 'transform', 'transformOrigin', 'fill', 'stroke', 'strokeWidth', 'objectFit', 'objectPosition', 'caretColor',
  ];
  const scrubURLs = value => value.replace(/url\((?:"[^"]*"|'[^']*'|[^)]*)\)/g, 'url([asset])');
  const style = computed => Object.fromEntries(properties.map(key => [key, scrubURLs(computed[key] ?? '')]));
  const visible = element => {
    for (let current = element; current; current = current.parentElement) {
      const value = getComputedStyle(current);
      if (value.display === 'none' || Number(value.opacity) === 0 || current === element && ['hidden', 'collapse'].includes(value.visibility)) return false;
      if (current === root) break;
    }
    return true;
  };
  const textValue = (text, computed) => computed.whiteSpace.startsWith('pre') ? text : text.replace(/[\t\n\f\r ]+/g, ' ').trim();
  const paths = new Map();
  const indexPaths = (element, path) => {paths.set(element, path.join('.')); [...element.children].forEach((child, index) => indexPaths(child, [...path, index]));};
  indexPaths(root, [0]);
  const anchorMap = new Map(), anchorIssues = [], repeatMap = new Map(), repeatedLists = [];
  for (const [name, query] of Object.entries(anchors)) {
    const matches = query === '$root' ? [root] : [...root.querySelectorAll(query)];
    if (matches.length !== 1) anchorIssues.push({name, matches: matches.length, elements: matches.map(element => paths.get(element))});
    for (const element of matches) anchorMap.set(element, name);
  }
  for (const group of repeats) {
    const container = [...anchorMap].find(([, name]) => name === group.container)?.[0];
    const items = container ? [...container.querySelectorAll(group.items)] : [];
    repeatedLists.push({name: group.container, container: container ? paths.get(container) : null, items: items.map(item => paths.get(item)), count: items.length});
    items.forEach((item, index) => {
      const frame = `${group.container}-${index}`, id = paths.get(item);
      anchorMap.set(item, frame); repeatMap.set(item, {list: group.container, item: id, index, role: group.role});
      for (const [part, query] of Object.entries(group.parts ?? {})) {
        const matches = [...item.querySelectorAll(query)];
        if (matches.length !== 1) anchorIssues.push({name: frame + '-' + part, matches: matches.length, elements: matches.map(element => paths.get(element))});
        for (const element of matches) {anchorMap.set(element, frame + '-' + part); repeatMap.set(element, {list: group.container, item: id, index, part});}
      }
    });
  }
  const parseRGBA = value => {
    const srgb = /^color\(srgb ([^)]+)\)$/.exec(value), rgb = /^rgba?\(([^)]+)\)$/.exec(value);
    if (value === 'transparent') return [0, 0, 0, 0];
    if (!srgb && !rgb) return null;
    const values = (srgb?.[1] ?? rgb[1]).replace('/', ' ').split(/[ ,]+/).filter(Boolean).map(Number);
    if (values.length < 3 || values.some(value => !Number.isFinite(value))) return null;
    return [values[0] * (srgb ? 255 : 1), values[1] * (srgb ? 255 : 1), values[2] * (srgb ? 255 : 1), values[3] ?? 1];
  };
  const effectivePaint = element => {
    const chain = []; for (let current = element; current; current = current.parentElement) chain.push(current);
    let result = [0, 0, 0, 0], opacity = 1, resolved = true;
    for (const layer of chain.reverse()) {
      const computed = getComputedStyle(layer), color = parseRGBA(computed.backgroundColor);
      opacity *= Number(computed.opacity);
      if (!color) {resolved = false; continue;}
      const alpha = color[3] + result[3] * (1 - color[3]);
      result = [...[0, 1, 2].map(index => alpha ? (color[index] * color[3] + result[index] * result[3] * (1 - color[3])) / alpha : 0), alpha];
    }
    return {effectiveBackgroundColor: resolved ? `rgba(${result.join(', ')})` : null, effectiveOpacity: opacity};
  };
  const nativeControl = element => element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement;
  const secretControl = element => nativeControl(element) && (element.type === 'password' || element.type === 'file' || /password|secret|token|otp|pin/i.test(element.name) || /password|one-time-code/.test(element.autocomplete ?? ''));
  const controlText = element => {
    if (element instanceof HTMLInputElement && ['checkbox', 'radio', 'range', 'file', 'color', 'hidden'].includes(element.type)) return '';
    if (element instanceof HTMLSelectElement) return [...element.selectedOptions].map(option => option.textContent.trim()).join(' ');
    return secretControl(element) ? '•'.repeat(Math.min(element.value.length, 1000)) : element.value;
  };
  const labelText = element => [...element.childNodes].map(child => child.nodeType === Node.TEXT_NODE ? child.textContent : child.nodeType === Node.ELEMENT_NODE && !nativeControl(child) && !['svg', 'img', 'canvas', 'style', 'script'].includes(child.localName) ? labelText(child) : '').join(' ').replace(/\s+/g, ' ').trim();
  const tokenCache = new Map();
  const renderedTokens = element => {
    if (tokenCache.has(element)) return tokenCache.get(element);
    const tokens = [];
    if (visible(element) && !['svg', 'img', 'canvas', 'style', 'script'].includes(element.localName)) {
      if (nativeControl(element)) {
        const text = controlText(element); if (text) tokens.push({text, rects: []});
      } else for (const child of element.childNodes) {
        if (child.nodeType === Node.ELEMENT_NODE) tokens.push(...renderedTokens(child));
        else if (child.nodeType === Node.TEXT_NODE) {
          const text = textValue(child.textContent ?? '', getComputedStyle(element));
          if (text) {const range = document.createRange(); range.selectNodeContents(child); tokens.push({text, rects: [...range.getClientRects()].filter(value => value.width && value.height).map(box)});}
        }
      }
    }
    tokenCache.set(element, tokens); return tokens;
  };
  const elements = [], strings = [];
  const visit = async (element, path) => {
    if (!visible(element)) return;
    const rect = element.getBoundingClientRect(), computed = getComputedStyle(element);
    const id = path.join('.'), tag = element.localName;
    if (rect.width > 0 && rect.height > 0) {
      const aria = Object.fromEntries([...element.attributes].filter(attribute => attribute.name.startsWith('aria-')).sort((a, b) => a.name.localeCompare(b.name)).map(attribute =>
        [attribute.name, ['aria-controls', 'aria-labelledby', 'aria-describedby', 'aria-owns'].includes(attribute.name) ? attribute.value.split(/\s+/).map(target => paths.get(document.getElementById(target)) ?? '[outside-screen]') : attribute.value]));
      const ownText = nativeControl(element) ? '' : [...element.childNodes].filter(child => child.nodeType === Node.TEXT_NODE).map(child => textValue(child.textContent ?? '', computed)).filter(Boolean).join(' ');
      const record = {id, parent: paths.get(element.parentElement) ?? null, tag, classes: [...element.classList].sort(),
        frame: anchorMap.get(element) ?? element.getAttribute('data-frame'), repeat: repeatMap.get(element), ownText, leaf: element.children.length === 0,
        normalizedText: renderedTokens(element).map(value => value.text).join(' '), descendantTextRects: renderedTokens(element).flatMap(value => value.rects),
        textRects: [], anchoredDescendants: [...anchorMap.keys()].filter(other => other !== element && element.contains(other)).length,
        aria, box: box(rect), style: style(computed), ...effectivePaint(element),
        role: element.getAttribute('role'), label: element.getAttribute('aria-label') ?? element.getAttribute('alt'),
        testId: element.getAttribute('data-testid'), focused: element === document.activeElement,
        disabled: 'disabled' in element ? Boolean(element.disabled) : element.getAttribute('aria-disabled') === 'true'};
      if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) {
        const secret = secretControl(element), displayed = controlText(element);
        record.input = {type: element.type, name: element.name, value: secret ? null : displayed,
          redacted: secret, length: element.value.length, placeholder: element.getAttribute('placeholder'),
          labels: [...(element.labels ?? [])].map(labelText)};
        if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) {
          record.input.selectionStart = element.selectionStart; record.input.selectionEnd = element.selectionEnd;
        }
        if (!secret && displayed) strings.push({id: `${id}:value`, owner: id, frame: record.frame, kind: 'value', text: displayed, box: box(rect), style: style(computed)});
        if (record.input.placeholder) strings.push({id: `${id}:placeholder`, owner: id, frame: record.frame, kind: 'placeholder', text: record.input.placeholder});
      }
      if (element instanceof HTMLImageElement) record.image = {kind: 'image', loaded: element.complete && element.naturalWidth > 0, fit: computed.objectFit};
      else if (tag === 'svg') record.image = {kind: 'icon', fit: computed.objectFit};
      elements.push(record);
      for (const pseudo of ['::before', '::after']) {
        const value = getComputedStyle(element, pseudo);
        if (value.content !== 'none' && value.content !== 'normal' && value.display !== 'none')
          elements.push({id: id + pseudo, parent: id, tag: pseudo, frame: record.frame ? record.frame + pseudo : null, style: style(value), content: scrubURLs(value.content)});
      }
      let textIndex = 0;
      for (const child of nativeControl(element) ? [] : element.childNodes) {
        if (child.nodeType !== Node.TEXT_NODE) continue;
        const text = textValue(child.textContent ?? '', computed);
        if (!text) continue;
        const range = document.createRange(); range.selectNodeContents(child);
        const rects = [...range.getClientRects()].filter(value => value.width > 0 && value.height > 0).map(box);
        record.textRects.push(...rects);
        if (rects.length) strings.push({id: `${id}:text:${textIndex++}`, owner: id, frame: record.frame, kind: 'text', text, rects, style: style(computed)});
      }
      if (record.label) strings.push({id: `${id}:label`, owner: id, frame: record.frame, kind: 'accessible-label', text: record.label});
    }
    if (nativeControl(element) || ['svg', 'canvas', 'img'].includes(tag)) return;
    for (let index = 0; index < element.children.length; index++) await visit(element.children[index], [...path, index]);
  };
  await visit(root, [0]);
  const viewport = {width: innerWidth, height: innerHeight, deviceScaleFactor: devicePixelRatio};
  return {version: 1, scope: 'screen', measurement: {renderer: 'dom', coordinateUnit: 'css-px'}, viewport, root: {x: round(bounds.x), y: round(bounds.y), width: round(bounds.width), height: round(bounds.height)},
    expectedViewport: expectedViewport ?? null,
    fullViewport: bounds.x === 0 && bounds.y === 0 && bounds.width === innerWidth && bounds.height === innerHeight,
    fontStatus: document.fonts.status, fontFaces: [...document.fonts].map(face => ({family: face.family, weight: face.weight, style: face.style, status: face.status})).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b))),
    documentFocused: document.hasFocus(), anchorIssues, repeatedLists, elements, strings};
}
