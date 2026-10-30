/** Read active rendered families from Chrome, never from a requested CSS chain.
 * The application/DOM is untouched. Inputs and mixed/unknown faces stay unavailable. */
const registrationName = value => {
  if (typeof value !== 'string' || !value.trim() || value.length > 200 || /[\r\n/\\]|(?:https?|file):|url\s*\(/i.test(value)) return undefined;
  const name = value.trim(), quote = name[0];
  if (quote === '"' || quote === "'") return name.at(-1) === quote && !name.slice(1, -1).includes(quote) ? name.slice(1, -1) : undefined;
  return /[,"']/.test(name) ? undefined : name;
};
export async function observeRenderedFonts(call, measured, selector, getMetadata = async () => ({complete: false, families: []})) {
  if (measured?.measurement?.renderer !== 'dom') throw new Error('A genuine DOM measurement is required');
  const result = structuredClone(measured);
  const text = result.elements.filter(node => node.ownText || node.input && (!node.input.type || ['text', 'password', 'email', 'search', 'tel', 'url', 'number', 'textarea'].includes(node.input.type)));
  if (text.length > 256) throw new Error('Rendered font observation requires a bounded text specimen');
  const ids = new Set(text.map(node => node.id));
  const parents = new Map(result.elements.map(node => [node.id, node.parent])), inputAncestors = new Set();
  for (const node of result.elements.filter(node => node.input)) {
    const seen = new Set();
    for (let parent = node.parent; parent && !seen.has(parent); parent = parents.get(parent)) {seen.add(parent); inputAncestors.add(parent);}
  }
  result.unavailable = (result.unavailable ?? []).filter(fact => fact.property !== 'resolvedFontFamily' || !ids.has(fact.element));
  const unavailable = (node, reason) => result.unavailable.push({element: node.id, property: 'resolvedFontFamily', reason});
  for (const node of text) {delete node.style.resolvedFontFamily; delete node.style.platformFontFamily;}
  const metadata = await getMetadata();
  let root;
  try {
    await call('DOM.enable'); await call('CSS.enable');
    const document = await call('DOM.getDocument', {depth: 0});
    root = (await call('DOM.querySelector', {nodeId: document.root.nodeId, selector})).nodeId;
  } catch { /* Each required text fact below remains explicitly unavailable. */ }
  for (const node of text) {
    if (node.input || inputAncestors.has(node.id)) {unavailable(node, 'Input glyph subtrees are not read by this observer'); continue;}
    if (!root || !/^0(?:\.\d+)*$/.test(node.id)) {unavailable(node, 'Rendered font node identity is unavailable'); continue;}
    try {
      const path = node.id.split('.').slice(1).map(index => ` > :nth-child(${Number(index) + 1})`).join('');
      const nodeId = path ? (await call('DOM.querySelector', {nodeId: root, selector: ':scope' + path})).nodeId : root;
      if (!nodeId) {unavailable(node, 'Rendered font node did not remain mounted'); continue;}
      const {fonts} = await call('CSS.getPlatformFontsForNode', {nodeId});
      const active = (fonts ?? []).filter(font => Number.isFinite(font.glyphCount) && font.glyphCount > 0);
      const families = [...new Set(active.map(font => font.familyName))];
      const name = families[0];
      if (families.length !== 1 || typeof name !== 'string' || !name.trim() || name.length > 200 || /[\r\n/\\]|(?:https?|file):|url\s*\(/i.test(name)) {
        unavailable(node, 'A single safe active rendered family is not observable'); continue;
      }
      node.style.platformFontFamily = name;
      const identities = new Set(); let invalidRegistration = false;
      for (const font of active) {
        if (font.isCustomFont === false) identities.add(font.familyName);
        else if (font.isCustomFont === true && metadata.complete) {
          for (const entry of metadata.families) if (entry.platformFamilies.includes(font.familyName)) {
            if (entry.nameId === 16) identities.add(entry.family);
            else if (entry.nameId === 1) for (const registration of metadata.registrations ?? []) if (registration.platformFamily === font.familyName) {
              const registered = registrationName(registration.family);
              if (registered) identities.add(registered); else invalidRegistration = true;
            }
          }
        }
      }
      if (invalidRegistration || identities.size !== 1 || active.some(font => font.isCustomFont !== false && (font.isCustomFont !== true || !metadata.complete || !metadata.families.some(entry => entry.platformFamilies.includes(font.familyName))))) {
        unavailable(node, 'Active font cannot be associated with verified typographic family metadata'); continue;
      }
      node.style.resolvedFontFamily = [...identities][0];
    } catch {unavailable(node, 'Active rendered family is not exposed by this observer');}
  }
  if (!result.unavailable.length) delete result.unavailable;
  return result;
}
