import {parseColor} from './color.mjs';

const finite = value => typeof value === 'number' && Number.isFinite(value);
const safe = value => typeof value === 'string' && value.length <= 512 && !/url\s*\(|(?:https?|file|data):/i.test(value);
const px = value => finite(value) ? `${value}px` : undefined;
export const declaredStyleSource = 'theme-resolved-stylesheet';
export function hasCompleteDeclaration(node) {
  const value = node?.diagnostics?.declaredStyle;
  return value?.source === declaredStyleSource && value.complete === true && node.diagnostics.flattenedStyle &&
    typeof node.diagnostics.flattenedStyle === 'object' && !Array.isArray(node.diagnostics.flattenedStyle);
}
export function declaredFontFamily(node) {
  const value = hasCompleteDeclaration(node) ? node.diagnostics.flattenedStyle.fontFamily : undefined;
  return safe(value) ? value : undefined;
}

/** RN flattened colours are already theme-resolved, never asset/prop strings. */
export function normalizeDeclaredColor(value) {
  let rgba;
  if (typeof value === 'string') {
    const match = /^#([\da-f]{3,4}|[\da-f]{6}|[\da-f]{8})$/i.exec(value);
    if (match) {
      const digits = match[1].length < 5 ? [...match[1]].map(c => c + c).join('') : match[1];
      rgba = [0, 2, 4].map(index => parseInt(digits.slice(index, index + 2), 16));
      rgba.push(digits.length === 8 ? parseInt(digits.slice(6), 16) / 255 : 1);
    } else rgba = parseColor(value);
  } else if (Number.isInteger(value) && value >= -2147483648 && value <= 0xffffffff) {
    // React Native numeric colours are ARGB, unlike CSS #RRGGBBAA.
    const argb = value >>> 0;
    rgba = [(argb >>> 16) & 255, (argb >>> 8) & 255, argb & 255, (argb >>> 24) / 255];
  }
  return rgba && rgba.every(finite) && rgba.slice(0, 3).every(v => v >= 0 && v <= 255) && rgba[3] >= 0 && rgba[3] <= 1 ? `rgba(${rgba.join(', ')})` : undefined;
}

/** Complete means the current final StyleSheet.flatten result, including omitted
 * RN own-view defaults. It does NOT prove an unregistered parent's declarations. */
export function normalizeNativeDeclaredStyle(flattened, {complete = false} = {}) {
  const style = {}, put = (key, value) => {if (value !== undefined) style[key] = value;};
  if (!flattened || typeof flattened !== 'object' || Array.isArray(flattened)) return {style, properties: []};
  const raw = flattened;
  for (const property of ['fontSize', 'lineHeight', 'letterSpacing']) put(property, px(raw[property]));
  for (const property of ['fontFamily', 'fontStyle', 'textTransform', 'textDecorationLine']) if (safe(raw[property])) put(property, raw[property]);
  if (finite(raw.fontWeight) || safe(raw.fontWeight)) put('fontWeight', String(raw.fontWeight === 'normal' ? 400 : raw.fontWeight === 'bold' ? 700 : raw.fontWeight));
  put('color', normalizeDeclaredColor(raw.color));
  put('backgroundColor', normalizeDeclaredColor(raw.backgroundColor ?? (complete ? 'transparent' : undefined)));
  if (finite(raw.opacity) && raw.opacity >= 0 && raw.opacity <= 1) put('opacity', String(raw.opacity));
  else if (raw.opacity === undefined && complete) put('opacity', '1');
  for (const corner of ['TopLeft', 'TopRight', 'BottomLeft', 'BottomRight']) {
    const value = raw[`border${corner}Radius`] ?? raw.borderRadius ?? (complete ? 0 : undefined);
    if (finite(value) && value >= 0) put(`border${corner}Radius`, px(value));
  }
  for (const side of ['Top', 'Right', 'Bottom', 'Left']) {
    const width = raw[`border${side}Width`] ?? raw.borderWidth ?? (complete ? 0 : undefined);
    if (finite(width) && width >= 0) put(`border${side}Width`, px(width));
    if (['solid', 'dashed', 'dotted'].includes(raw.borderStyle)) put(`border${side}Style`, raw.borderStyle);
    else if (raw.borderStyle === undefined && complete) put(`border${side}Style`, 'solid');
    put(`border${side}Color`, normalizeDeclaredColor(raw[`border${side}Color`] ?? raw.borderColor));
  }
  const shadow = (prefix, opacity) => {
    const color = normalizeDeclaredColor(raw[prefix + 'Color']), radius = raw[prefix + 'Radius'];
    const x = raw[prefix + 'OffsetX'], y = raw[prefix + 'OffsetY'];
    if (color && finite(radius) && radius >= 0 && finite(x) && finite(y) && finite(opacity) && opacity >= 0 && opacity <= 1) {
      const rgba = parseColor(color); rgba[3] *= opacity;
      return `rgba(${rgba.join(', ')}) ${x}px ${y}px ${radius}px${prefix === 'shadow' ? ' 0px' : ''}`;
    }
    return undefined;
  };
  put('boxShadow', shadow('shadow', raw.shadowOpacity));
  if (complete && !Object.keys(raw).some(key => key.startsWith('shadow')) && (raw.elevation === undefined || raw.elevation === 0) && raw.boxShadow === undefined) put('boxShadow', 'none');
  put('textShadow', shadow('textShadow', 1));
  if (complete && !Object.keys(raw).some(key => key.startsWith('textShadow'))) put('textShadow', 'none');
  return {style, properties: Object.keys(style)};
}

/** Same scope as DOM parentElements: screen/view ancestry, no OS compositor.
 * Transparent default/opacity1 are legal only for complete registered layers. */
export function normalizeNativeDeclaredAncestry(node, nodes) {
  let current = node, opacity = 1, all = true;
  const chain = [], seen = new Set();
  while (current && !seen.has(current.id)) {
    seen.add(current.id);
    if (!hasCompleteDeclaration(current)) {all = false; chain.push(null);}
    else {
      const own = normalizeNativeDeclaredStyle(current.diagnostics.flattenedStyle, {complete: true}).style;
      const factor = Number(own.opacity);
      if (!finite(factor)) all = false; else opacity *= factor;
      chain.push(own.backgroundColor ? parseColor(own.backgroundColor) : null);
    }
    if (current.parent == null) break;
    current = nodes.get(current.parent);
    if (!current) {all = false; chain.push(null);}
  }
  if (current && current.parent != null && seen.has(current.parent)) all = false;
  let result = [0, 0, 0, 0], resolved = true;
  for (const color of chain.reverse()) {
    if (!color) {resolved = false; continue;}
    if (color[3] === 1) resolved = true;
    const alpha = color[3] + result[3] * (1 - color[3]);
    result = [0, 1, 2].map(index => alpha ? (color[index] * color[3] + result[index] * result[3] * (1 - color[3])) / alpha : 0).concat(alpha);
  }
  return {effectiveOpacity: all ? opacity : undefined, effectiveBackgroundColor: resolved ? `rgba(${result.join(', ')})` : undefined};
}

/** Project NEW explicitly declared facts into a comparison-only clone. Legacy
 * diagnostics alone never change scores; raw files are never mutated. */
export function prepareNativeDeclarations(screen) {
  if (screen?.measurement?.renderer !== 'react-native' || !screen.elements?.some(hasCompleteDeclaration)) return screen;
  const nodes = new Map(screen.elements.map(node => [node.id, node])), provided = new Map();
  const elements = screen.elements.map(node => {
    if (!hasCompleteDeclaration(node)) return node;
    const own = normalizeNativeDeclaredStyle(node.diagnostics.flattenedStyle, {complete: true});
    const ancestry = normalizeNativeDeclaredAncestry(node, nodes);
    const fields = Object.keys(ancestry).filter(key => ancestry[key] !== undefined);
    provided.set(node.id, new Set([...own.properties, ...fields]));
    // Declared family is accepted under this source policy; it is never stored
    // as an observed resolvedFontFamily or a claim that a font loaded.
    if (declaredFontFamily(node)) provided.get(node.id).add('resolvedFontFamily');
    return {...node, style: {...node.style, ...own.style}, ...Object.fromEntries(fields.map(key => [key, ancestry[key]]))};
  });
  return {...screen, elements, unavailable: screen.unavailable?.filter(fact => !provided.get(fact.element)?.has(fact.property))};
}
