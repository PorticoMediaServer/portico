// CIEDE2000, kL=kC=kH=1. Equations and published verification data:
// https://hajim.rochester.edu/ece/sites/gsharma/ciede2000/ (Sharma, Wu, Dalal 2005).
const rad = degrees => degrees * Math.PI / 180, deg = radians => radians * 180 / Math.PI;
export function deltaE2000([l1, a1, b1], [l2, a2, b2]) {
  const c1 = Math.hypot(a1, b1), c2 = Math.hypot(a2, b2), c = (c1 + c2) / 2;
  const g = .5 * (1 - Math.sqrt(c ** 7 / (c ** 7 + 25 ** 7)));
  const ap1 = (1 + g) * a1, ap2 = (1 + g) * a2;
  const cp1 = Math.hypot(ap1, b1), cp2 = Math.hypot(ap2, b2);
  const hue = (a, b) => a === 0 && b === 0 ? 0 : (deg(Math.atan2(b, a)) + 360) % 360;
  const h1 = hue(ap1, b1), h2 = hue(ap2, b2), dl = l2 - l1, dc = cp2 - cp1;
  let dh = cp1 * cp2 === 0 ? 0 : h2 - h1;
  if (dh > 180) dh -= 360; else if (dh < -180) dh += 360;
  const dH = 2 * Math.sqrt(cp1 * cp2) * Math.sin(rad(dh / 2));
  const lp = (l1 + l2) / 2, cp = (cp1 + cp2) / 2;
  const hp = cp1 * cp2 === 0 ? h1 + h2 : Math.abs(h1 - h2) <= 180 ? (h1 + h2) / 2 : (h1 + h2 + (h1 + h2 < 360 ? 360 : -360)) / 2;
  const t = 1 - .17 * Math.cos(rad(hp - 30)) + .24 * Math.cos(rad(2 * hp)) + .32 * Math.cos(rad(3 * hp + 6)) - .2 * Math.cos(rad(4 * hp - 63));
  const sl = 1 + .015 * (lp - 50) ** 2 / Math.sqrt(20 + (lp - 50) ** 2), sc = 1 + .045 * cp, sh = 1 + .015 * cp * t;
  const rt = -2 * Math.sqrt(cp ** 7 / (cp ** 7 + 25 ** 7)) * Math.sin(rad(60 * Math.exp(-(((hp - 275) / 25) ** 2))));
  return Math.sqrt((dl / sl) ** 2 + (dc / sc) ** 2 + (dH / sh) ** 2 + rt * (dc / sc) * (dH / sh));
}
export function parseColor(value) {
  if (value === 'transparent') return [0, 0, 0, 0];
  const srgb = /^color\(srgb ([^)]+)\)$/.exec(value);
  if (srgb) {
    const parts = srgb[1].replace('/', ' ').split(/\s+/).filter(Boolean).map(Number);
    return parts.length >= 3 && parts.length <= 4 && parts.every(Number.isFinite) ? [parts[0] * 255, parts[1] * 255, parts[2] * 255, parts[3] ?? 1] : null;
  }
  const match = /^rgba?\(([^)]+)\)$/.exec(value);
  if (!match) return null;
  const parts = match[1].replace('/', ' ').split(/[ ,]+/).filter(Boolean);
  if (parts.length < 3 || parts.length > 4) return null;
  const rgba = parts.map((part, index) => Number.parseFloat(part) * (part.endsWith('%') ? index === 3 ? .01 : 2.55 : 1));
  if (rgba.some(value => !Number.isFinite(value))) return null;
  return [...rgba.slice(0, 3), rgba[3] ?? 1];
}
export function srgbToLab([r, g, b]) {
  const [rl, gl, bl] = [r, g, b].map(value => {value /= 255; return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4;});
  const xyz = [(.4124564 * rl + .3575761 * gl + .1804375 * bl) / .95047,
    .2126729 * rl + .7151522 * gl + .072175 * bl, (.0193339 * rl + .119192 * gl + .9503041 * bl) / 1.08883];
  const [x, y, z] = xyz.map(value => value > (6 / 29) ** 3 ? Math.cbrt(value) : value / (3 * (6 / 29) ** 2) + 4 / 29);
  return [116 * y - 16, 500 * (x - y), 200 * (y - z)];
}
export function colorDifference(left, right) {
  const a = parseColor(left), b = parseColor(right);
  if (!a || !b) return null;
  return {deltaE2000: a[3] === 0 && b[3] === 0 ? 0 : deltaE2000(srgbToLab(a), srgbToLab(b)), alpha: Math.abs(a[3] - b[3])};
}
