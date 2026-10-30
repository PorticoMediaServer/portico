/** Measured strings are data. This tool neither adds copy nor edits catalogues. */
export function stringInventory(screen) {
  if (!['screen', 'component'].includes(screen?.scope) || !Array.isArray(screen.strings)) throw new Error('A screen measurement or explicit component measurement is required');
  const inventory = new Map();
  for (const value of screen.strings) {
    if (typeof value.text !== 'string' || typeof value.id !== 'string') throw new Error('Invalid measured string');
    // Input values and DOM text are both visible copy. Accessibility labels and
    // placeholders stay distinct so a label cannot excuse missing drawn text.
    const channel = value.kind === 'text' || value.kind === 'value' ? 'visible' : value.kind;
    const key = JSON.stringify([channel, value.text]);
    if (!inventory.has(key)) inventory.set(key, {channel, text: value.text, occurrences: []});
    inventory.get(key).occurrences.push(value.id);
  }
  return [...inventory.values()];
}

export function compareStrings(reference, actual) {
  const expected = stringInventory(reference), observed = stringInventory(actual);
  const key = value => JSON.stringify([value.channel, value.text]);
  const actualByKey = new Map(observed.map(value => [key(value), value]));
  const expectedByKey = new Map(expected.map(value => [key(value), value]));
  const differences = [];
  let required = 0, matched = 0;
  for (const value of expected) {
    const count = value.occurrences.length, actualCount = actualByKey.get(key(value))?.occurrences.length ?? 0;
    required += count; matched += Math.min(count, actualCount);
    if (actualCount < count) differences.push({kind: 'missing-string', channel: value.channel, text: value.text, count: count - actualCount});
  }
  for (const value of observed) {
    const count = value.occurrences.length, expectedCount = expectedByKey.get(key(value))?.occurrences.length ?? 0;
    if (count > expectedCount) differences.push({kind: 'extra-string', channel: value.channel, text: value.text, count: count - expectedCount});
  }
  return {required, matched, differences};
}

/** Exact catalogue matches only: no punctuation, case or Unicode normalization. */
export function catalogueMatches(screen, catalogue) {
  const values = new Map();
  for (const [key, text] of Object.entries(catalogue)) {
    if (typeof text !== 'string') throw new Error('A flat string catalogue is required');
    const keys = values.get(text) ?? []; keys.push(key); values.set(text, keys);
  }
  return stringInventory(screen).map(value => ({...value, catalogueKeys: values.get(value.text) ?? []}));
}

export function mapScreenStrings(screen, catalogue, {keys = {}, data = {}, preferredKeys = [], messageSlots = [], dataValues = []} = {}, formatMessage) {
  const index = new Map();
  for (const [key, value] of Object.entries(catalogue)) if (typeof value === 'string') {const ids = index.get(value) ?? []; ids.push(key); index.set(value, ids);}
  return screen.strings.map(value => {
    const slot = data[value.frame];
    if (slot) return {...value, status: value.text === slot.value ? 'data' : 'data-mismatch', dataSlot: slot.name, expected: slot.value};
    const literal = dataValues.find(slot => typeof slot.name === 'string' && slot.value === value.text);
    if (literal) return {...value, status: 'data', dataSlot: literal.name, expected: literal.value};
    const messages = typeof formatMessage === 'function' ? messageSlots.filter(slot => (!slot.frame || slot.frame === value.frame) && typeof catalogue[slot.key] === 'string' && formatMessage(slot.key, slot.values) === value.text) : [];
    if (messages.length) {const ids = [...new Set(messages.map(slot => slot.key))]; return {...value, status: ids.length === 1 ? 'mapped' : 'ambiguous', catalogueKeys: ids, messageSlots: messages.map(slot => slot.name)};}
    const selected = (Array.isArray(keys[value.frame]) ? keys[value.frame] : [keys[value.frame]]).filter(key => key && catalogue[key] === value.text);
    if (!selected.length) selected.push(...preferredKeys.filter(key => catalogue[key] === value.text));
    const candidates = selected.length ? selected : index.get(value.text) ?? [];
    return {...value, status: candidates.length === 1 ? 'mapped' : candidates.length > 1 ? 'ambiguous' : 'unmapped', catalogueKeys: candidates};
  });
}

export function compareCatalogueStrings(reference, actual, catalogue, adapter, cascadeRoots = {reference: [], actual: []}, formatMessage) {
  const keep = (value, roots) => !roots.some(root => value.owner && (value.owner === root || value.owner.startsWith(root + '.')));
  const frame = mapScreenStrings(reference, catalogue, adapter, formatMessage).filter(value => keep(value, cascadeRoots.reference)), built = mapScreenStrings(actual, catalogue, adapter, formatMessage).filter(value => keep(value, cascadeRoots.actual)), differences = [];
  for (const slot of adapter.messageSlots ?? []) if (!slot.name || typeof catalogue[slot.key] !== 'string' || typeof formatMessage !== 'function') differences.push({kind: 'message-slot-invalid', label: slot.name ?? 'Unnamed message slot', key: slot.key});
  for (const value of frame) if (['unmapped', 'ambiguous', 'data-mismatch'].includes(value.status)) differences.push({kind: 'frame-string-' + value.status, label: value.frame || value.id, text: value.text, catalogueKeys: value.catalogueKeys ?? [], dataSlot: value.dataSlot});
  const frameText = new Set(frame.map(value => value.text));
  for (const value of built) if (value.status === 'data-mismatch' || value.status === 'ambiguous' || value.status === 'unmapped' && !frameText.has(value.text)) differences.push({kind: 'built-string-' + value.status, label: value.frame || value.id, text: value.text, catalogueKeys: value.catalogueKeys ?? [], dataSlot: value.dataSlot});
  const staticVisible = values => ({scope: 'screen', strings: values.filter(value => value.status !== 'data' && value.kind !== 'accessible-label' && value.kind !== 'placeholder')});
  const copy = compareStrings(staticVisible(frame), staticVisible(built)); differences.push(...copy.differences);
  return {frame, built, requiredVisibleStrings: copy.required, matchedVisibleStrings: copy.matched, differences};
}
