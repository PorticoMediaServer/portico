// Strict kit boundary: raw colour, typography and stacking values belong in kit.
// Undefined custom properties, off-grid inline spacing and uncatalogued copy fail.
// --root supplies an isolated fixture directory for the rule tests; no baseline exemptions.
import {readdirSync, readFileSync, statSync} from 'node:fs';
import {join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';
import {parseSync} from 'rolldown/utils';

const design = await import('../../packages/design/src/index.ts');
const grid = new Set(design.spacingGrid);
const argv = new Set(process.argv.slice(2));
const strict = argv.has('--strict') || process.env.LINT_DESIGN_STRICT === '1';
const verbose = argv.has('--verbose');
const root = process.argv.includes('--root') ? process.argv[process.argv.indexOf('--root') + 1] : fileURLToPath(new URL('../src', import.meta.url));

const files = [];
const walk = dir => {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p);
    else if (/\.(tsx?|css)$/.test(name)) files.push(p);
  }
};
walk(root);

const problems = [];
const warnings = [];
const boundaryRules = [
  [/#[0-9a-fA-F]{3,8}\b/, 'hex colour'],
  [/\b(rgba?|hsla?)\(/, 'rgb/hsl colour'],
  [/\bfontFamily\s*:\s*['"`]/, 'fontFamily literal (use kit typography)'],
  [/(?:font-size|font-weight|line-height|letter-spacing)\s*:\s*-?\d/, 'raw typography (use type tokens)'],
  [/\b(?:fontSize|fontWeight|lineHeight|letterSpacing)\s*:\s*(?:['"`]|-?\d)/, 'raw inline typography (use kit typography)'],
  [/font-family\s*:(?!\s*(?:var\(|(?:inherit|initial|unset|revert|revert-layer)\s*[;}]))/, 'font-family literal (use --font tokens)'],
  [/z-index\s*:\s*(?:[2-9]|\d{2,})/, 'numeric z-index (use --layer-* tokens; 0 and 1 are local stacking)'],
];

// Every custom property defined anywhere in the app (CSS declarations, and
// inline style keys such as {'--dialog-width': …} or style.setProperty('--x', …)).
// Radix sets its own --radix-* properties at runtime.
// Spec anchors in comments are documentation, not CSS colors. Mask only real
// comments, preserving line positions and quoted CSS strings/URLs for diagnostics.
function withoutComments(text, file) {
  const ranges = [];
  if (file.endsWith('.css')) {
    let quote = '';
    for (let i = 0; i < text.length; i++) {
      if (quote) {if (text[i] === '\\') i++; else if (text[i] === quote) quote = ''; continue;}
      if (text[i] === '"' || text[i] === "'") {quote = text[i]; continue;}
      if (text[i] === '/' && text[i + 1] === '*') {
        const end = text.indexOf('*/', i + 2);
        ranges.push({start: i, end: end < 0 ? text.length : end + 2});
        i = ranges.at(-1).end - 1;
      }
    }
  } else {
    const parsed = parseSync(file.endsWith('.tsx') ? 'design.tsx' : 'design.ts', text, {sourceType: 'unambiguous'});
    if (parsed.errors.length) throw new Error(`Design lint cannot parse ${file}`);
    ranges.push(...parsed.comments);
  }
  for (const {start, end} of ranges.reverse()) text = text.slice(0, start) + text.slice(start, end).replace(/[^\r\n]/g, ' ') + text.slice(end);
  return text;
}
const defined = new Set();
const sources = new Map();
for (const file of files) {
  const text = withoutComments(readFileSync(file, 'utf8'), file);
  sources.set(file, text);
  const decl = file.endsWith('.css') ? /(--[A-Za-z0-9_-]+)\s*:/g : /['"`](--[A-Za-z0-9_-]+)['"`]\s*[:,]/g;
  for (const m of text.matchAll(decl)) defined.add(m[1]);
}

const spacingProp = /\b(padding|paddingTop|paddingRight|paddingBottom|paddingLeft|paddingInline|paddingBlock|margin|marginTop|marginRight|marginBottom|marginLeft|marginInline|marginBlock|gap|rowGap|columnGap)\s*:\s*('([^']*)'|"([^"]*)"|-?\d+(?:\.\d+)?)/g;
function offGrid(raw) {
  const values = [];
  if (/^-?\d+(?:\.\d+)?$/.test(raw)) values.push(Math.abs(Number(raw)));
  else for (const m of raw.matchAll(/(-?\d+(?:\.\d+)?)px/g)) values.push(Math.abs(Number(m[1])));
  return values.filter(v => !grid.has(v));
}

for (const [file, text] of sources) {
  const rel = relative(root, file);
  const insideKit = rel.startsWith('kit/');
  text.split('\n').forEach((line, i) => {
    const at = `${rel}:${i + 1}`;
    if (!insideKit) for (const [re, label] of boundaryRules) if (re.test(line)) problems.push(`${at}: ${label}: ${line.trim().slice(0, 100)}`);
    // A fallback is only consulted when the outer property is undefined: var(--defined, var(--x)) is fine.
    const effective = line.replace(/var\(\s*(--[A-Za-z0-9_-]+)\s*,\s*var\(\s*--[A-Za-z0-9_-]+\s*\)\s*\)/g, (whole, outer) => (defined.has(outer) ? `var(${outer}, fallback)` : whole));
    for (const m of effective.matchAll(/var\(\s*(--[A-Za-z0-9_-]+)\s*\)/g)) {
      if (!defined.has(m[1]) && !m[1].startsWith('--radix-')) problems.push(`${at}: undefined custom property ${m[1]} with no fallback (define it, fix the name, or add a fallback)`);
    }
    if (!file.endsWith('.tsx') || insideKit) return;
    const z = /\bzIndex\s*:\s*(\d+)/.exec(line);
    if (z && Number(z[1]) > 1) warnings.push({rel, at, text: `zIndex: ${z[1]} (use a --layer-* token via CSS)`});
    for (const m of line.matchAll(spacingProp)) {
      const bad = offGrid(m[3] ?? m[4] ?? m[2]);
      if (bad.length) warnings.push({rel, at, text: `${m[1]}: ${m[2]} is off the 4-pt grid (${bad.join(', ')}px)`});
    }
  });
}

// Hard-coded user-facing strings in screens/: a ratchet against the baseline
// in packages/i18n (warn-only until the web screens move onto the catalogue).
{
  const {spawnSync} = await import('node:child_process');
  const lint = fileURLToPath(new URL('../../packages/i18n/scripts/lint-strings.mjs', import.meta.url));
  const baseline = fileURLToPath(new URL('./next-strings-baseline.json', import.meta.url));
  const result = spawnSync(process.execPath, [lint, root, baseline, '--all', ...(strict ? ['--strict'] : [])], {stdio: 'inherit'});
  if (strict && result.status) problems.push('hard-coded strings above the i18n baseline (see above)');
}

if (warnings.length) {
  if (verbose) for (const w of warnings) console.warn(`warning ${w.at}: ${w.text}`);
  else {
    const byFile = new Map();
    for (const w of warnings) byFile.set(w.rel, (byFile.get(w.rel) ?? 0) + 1);
    for (const w of warnings.filter(w => w.text.startsWith('zIndex'))) console.warn(`warning ${w.at}: ${w.text}`);
    const top = [...byFile].sort((a, b) => b[1] - a[1]).slice(0, 12).map(([f, n]) => `${f} (${n})`).join(', ');
    console.warn(`warning: ${warnings.length} design warning(s) in ${byFile.size} file(s): ${top}${byFile.size > 12 ? ', …' : ''}. Run with --verbose for the list.`);
  }
  if (strict) problems.push(`${warnings.length} design warning(s) (strict mode)`);
}
if (problems.length) {
  console.error(problems.join('\n'));
  console.error(`\n${problems.length} design boundary violation(s).`);
  process.exit(1);
}
console.log('design boundary clean');
