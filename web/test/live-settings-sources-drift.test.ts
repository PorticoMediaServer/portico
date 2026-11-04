import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('Live TV & Channels shows the server band\'s health line: its self-loading page still reads the header facts', () => {
  const controller = read('../src/screens/settings/controller.ts'), route = read('../src/screens/settings/Settings.route.tsx');
  assert.match(controller, /loadServerHeader = \(section: SettingsSection\): Promise<void> => \{/);
  assert.match(controller, /load = \(section: SettingsSection\): Promise<void> => \{\n    if \(!this\.current\(\) \|\| !this\.sectionVisible\(section\)\) return Promise\.resolve\(\);\n    void this\.loadServerHeader\(section\);/);
  assert.match(route, /\(section\.id === 'remote-sources' \|\| section\.id === 'live' && !!live\) \? controller\.loadServerHeader\(section\) : source\.load\(section\),/);
});

test('Sources draws live-owner d2: the lede under the heading, and an empty card with a TV tile and the white Add source', () => {
  const list = read('../src/screens/live/owner/SourceList.tsx'), css = read('../src/screens/live/owner/owner.module.css');
  assert.match(list, /<p className=\{styles\.lede\}>\{t\('next\.liveOwner\.sourcesLede'\)\}<\/p>/);
  assert.match(list, /<span className=\{styles\.emptyIcon\} aria-hidden="true"><Icon name="i-tv" size=\{24\}\/><\/span><h4>\{t\('live\.noSources'\)\}<\/h4>/);
  assert.match(list, /<Button variant="primary" size="small" icon="i-plus" onClick=\{\(\) => setAddOpen\(true\)\}>/);
  assert.match(css, /\.empty \{ text-align:center;padding:44px 24px 40px;border-radius:14px;background:var\(--color-raised\); \}/);
});
