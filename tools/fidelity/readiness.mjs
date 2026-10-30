/** Shared font readiness facts; no guessed fallback or font-source URLs. */
export function fontReadiness(screen, side) {
  const differences = [];
  const required = new Map(screen.strings.filter(value => value.style).map(value => [value.style.fontFamily + '|' + value.style.fontWeight, value.style]));
  for (const style of required.values()) {
    const family = style.fontFamily.split(',')[0].trim().replace(/^['"]|['"]$/g, ''), weight = Number(style.fontWeight);
    if (['system-ui', 'sans-serif', 'serif', 'monospace'].includes(family)) continue;
    // CSS selects an available weight within the same family; requested 900 need
    // not have a separate face when 800 is the matched face. Keep requested CSS
    // weight unchanged in the measurement/comparator.
    // https://www.w3.org/TR/css-fonts-4/#font-style-matching
    const rank = value => weight > 500 ? [value >= weight ? 0 : 1, value >= weight ? value : -value]
      : weight < 400 ? [value <= weight ? 0 : 1, value <= weight ? -value : value]
        : value >= weight && value <= 500 ? [0, value] : value < weight ? [1, -value] : [2, value];
    const candidates = screen.fontFaces.filter(face => face.family.replace(/^['"]|['"]$/g, '') === family).map(face => {
      const range = face.weight.split(/\s+/).map(value => value === 'normal' ? 400 : value === 'bold' ? 700 : Number(value)), low = range[0], high = range[1] ?? low;
      return {face, rank: rank(Math.max(low, Math.min(high, weight)))};
    }).filter(value => value.rank.every(Number.isFinite)).sort((a, b) => a.rank[0] - b.rank[0] || a.rank[1] - b.rank[1]);
    const selected = candidates[0]?.rank;
    if (!selected || !candidates.some(value => value.rank[0] === selected[0] && value.rank[1] === selected[1] && value.face.status === 'loaded')) differences.push({kind: 'font-not-loaded', label: `${side} ${family} ${weight}`});
  }
  return differences;
}
