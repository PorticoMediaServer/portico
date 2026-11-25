import test from 'node:test';
import assert from 'node:assert/strict';
import {cpSync, mkdtempSync, readFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {renderTokens, tokenCssPath} from '../../packages/design/scripts/tokens.mjs';

const webSrc = fileURLToPath(new URL('../src', import.meta.url));

test('App.tsx and HostedServicesHost.tsx lazy-load PlaybackHost and ServicesApp', () => {
  const appSource = readFileSync(join(webSrc, 'App.tsx'), 'utf8');
  assert.doesNotMatch(appSource, /^import\s+\{[^}]*PlaybackHost[^}]*\}\s+from\s+['"]\.\/shell\/Playback['"]/m);
  assert.match(appSource, /import\(['"]\.\/shell\/Playback['"]\)/);

  const hostedSource = readFileSync(join(webSrc, 'screens/accounts/HostedServicesHost.tsx'), 'utf8');
  assert.doesNotMatch(hostedSource, /^import\s+\{[^}]*ServicesApp[^}]*\}\s+from\s+['"]\.\.\/\.\.\/services\/ServicesApp['"]/m);
  assert.match(hostedSource, /import\(['"]\.\.\/\.\.\/services\/ServicesApp['"]\)/);
  assert.match(hostedSource, /<Suspense\b/);
});

test('tokens.css prunes dead --spec-* tokens while staying byte-identical to renderTokens()', () => {
  const css = renderTokens();
  assert.equal(readFileSync(tokenCssPath, 'utf8'), css);
  const specDecls = [...css.matchAll(/^\s*(--spec-[A-Za-z0-9_-]+):/gm)].map(match => match[1]);
  assert.ok(specDecls.length < 50, `expected pruned --spec-* declarations, got ${specDecls.length}`);
  assert.ok(css.includes('--spec-focus-ring-width-web:'), 'referenced web spec token kept');
  assert.ok(css.includes('--spec-hero-actions-gap-phone:'), 'referenced hero gap token kept');
  assert.doesNotMatch(css, /--spec-episode-fallback-brightness:\s*0\.42px;/, 'unit bug must never be emitted');
});

test('tokens.mjs emits unitless brightness in unpruned isolated generation', () => {
  const dir = mkdtempSync(join(tmpdir(), 'portico-tokens-unit-'));
  try {
    cpSync(fileURLToPath(new URL('../../packages/design', import.meta.url)), join(dir, 'packages/design'), {recursive: true});
    const script = join(dir, 'packages/design/scripts/tokens.mjs');
    const out = join(dir, 'web/src/kit/tokens.css');
    const run = spawnSync(process.execPath, [script], {cwd: tmpdir(), encoding: 'utf8'});
    assert.equal(run.status, 0, run.stderr);
    const fullCss = readFileSync(out, 'utf8');
    assert.ok(fullCss.includes('--spec-episode-fallback-brightness: 0.42;'), 'brightness must be unitless');
    assert.doesNotMatch(fullCss, /--spec-episode-fallback-brightness:\s*0\.42px;/);
  } finally {
    rmSync(dir, {recursive: true, force: true});
  }
});
