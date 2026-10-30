import {PdfInputError} from './engine.ts';

// PDF structure roles, following PDF.js's structural layer. Links, forms and
// annotations deliberately gain no interactive role or action in this reader.
const roles: Readonly<Record<string, string>> = {
  Part: 'group', Sect: 'group', Div: 'group', Aside: 'note', NonStruct: 'none',
  H: 'heading', FENote: 'note', Sub: 'group', L: 'list', LI: 'listitem',
  Table: 'table', TR: 'row', TH: 'columnheader', TD: 'cell', THead: 'rowgroup',
  TBody: 'rowgroup', TFoot: 'rowgroup', Figure: 'figure',
};
let sequence = 0;
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

/** A bounded semantic projection of PDF.js getStructTree(), owning only this
 * page's real marked-content spans. PDF strings are attributes, never HTML. */
export function renderPdfStructure(tree: unknown, textLayer: HTMLElement): HTMLElement | null {
  if (!record(tree) || !Array.isArray(tree.children)) return null;
  const owner = textLayer.ownerDocument, prefix = `portico-pdf-structure-${++sequence}`;
  const content = new Map<string, HTMLElement>(), duplicate = new Set<string>(), claimed = new Set<string>();
  for (const element of textLayer.querySelectorAll<HTMLElement>('.markedContent[id]')) {
    if (content.has(element.id)) duplicate.add(element.id);
    else content.set(element.id, element);
  }
  let nodes = 0, bytes = 0;
  const label = (value: unknown, maximum: number): string | undefined => {
    if (typeof value !== 'string') return;
    bytes += value.length * 2;
    if (value.length > maximum || bytes > 2 * 1024 * 1024) throw new PdfInputError('pdf_limit');
    return value.replaceAll('\0', '');
  };
  const visit = (value: unknown, depth: number): HTMLElement | null => {
    if (++nodes > 50000 || depth > 64) throw new PdfInputError('pdf_limit');
    if (!record(value)) return null;
    if ('type' in value) {
      if (value.type !== 'content' || typeof value.id !== 'string' || duplicate.has(value.id) || claimed.has(value.id)) return null;
      const target = content.get(value.id);
      if (!target || target.closest('[aria-hidden="true"]')) return null;
      claimed.add(value.id);
      // Namespace IDs per mounted page; never let a PDF own unrelated app DOM.
      target.id = `${prefix}-${claimed.size}`;
      const element = owner.createElement('span'); element.setAttribute('aria-owns', target.id); return element;
    }
    if (typeof value.role !== 'string' || !Array.isArray(value.children)) return null;
    const element = owner.createElement('span'), heading = /^H([1-6])$/.exec(value.role);
    let role: string | undefined = heading ? 'heading' : Object.hasOwn(roles, value.role) ? roles[value.role] : undefined;
    const alt = label(value.alt, 65536), lang = label(value.lang, 256);
    if (alt !== undefined) {
      if (value.role === 'Figure' || value.role === 'Formula') role = 'img';
      else role ||= 'group';
      element.setAttribute('aria-label', alt);
    }
    if (role) element.setAttribute('role', role);
    if (role === 'heading') element.setAttribute('aria-level', heading?.[1] ?? '2');
    if (lang !== undefined) element.setAttribute('lang', lang);
    for (const child of value.children) {
      const rendered = visit(child, depth + 1);
      if (rendered) element.append(rendered);
    }
    return element.childNodes.length || alt !== undefined ? element : null;
  };
  const result = visit(tree, 0);
  if (result) result.dataset.readerStructure = '';
  return result;
}
