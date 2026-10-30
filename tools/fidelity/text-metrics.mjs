const typography = ['fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'lineHeight', 'letterSpacing', 'color', 'textAlign', 'textTransform'];
const visible = screen => screen.strings.filter(value => value.kind === 'text' || value.kind === 'value');
const point = value => value.rects?.[0] ?? value.box ?? {x: 0, y: 0, width: 0, height: 0};
const distance = (left, right) => {
  const a = point(left), b = point(right);
  return (a.x - b.x) ** 2 + (a.y - b.y) ** 2;
};

/** Match exact text occurrences; report facts rather than guessing a rendering cause. */
export function compareTextMetrics(reference, actual) {
  const candidates = visible(actual), used = new Set(), diagnostics = [], unassessed = [];
  let matched = 0;
  for (const expected of visible(reference)) {
    const observed = candidates.filter(value => value.text === expected.text && !used.has(value.id)).sort((a, b) => distance(expected, a) - distance(expected, b))[0];
    if (!observed) continue;
    matched++; used.add(observed.id);
    const identity = {text: expected.text, referenceElement: expected.id, actualElement: observed.id};
    if (!expected.rects || !observed.rects) unassessed.push({...identity, fact: 'native-input-glyph-bounds', reason: 'DOM exposes the input host, not its native value glyph rectangles; native glyph rectangles remain unassessed.'});
    else if (JSON.stringify(expected.rects) !== JSON.stringify(observed.rects)) diagnostics.push({...identity, fact: 'text-rectangles', expected: expected.rects, actual: observed.rects});
    if (expected.style && observed.style) for (const property of typography) {
      if (expected.style[property] !== observed.style[property]) diagnostics.push({...identity, fact: property, expected: expected.style[property], actual: observed.style[property]});
    }
  }
  return {scored: false, expectedOccurrences: visible(reference).length, matchedOccurrences: matched, diagnostics, unassessed};
}
