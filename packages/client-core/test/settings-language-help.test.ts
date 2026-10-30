import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsStructure} from '../src/presentation/index.ts';
import {createI18n} from '../../i18n/src/index.ts';

test('Language carries the drawn audio and subtitle guidance on every client', () => {
  const i18n = createI18n({locale: 'en-US'});
  for (const platform of ['web', 'phone', 'tv'] as const) {
    const page = settingsStructure({owner: false, platform, capabilities: {}})
      .headings.flatMap(heading => heading.pages).find(page => page.id === 'region')!;
    const row = page.sections.flatMap(section => section.rows)
      .find(row => row.kind === 'preference' && row.key === 'region.locale')!;
    assert.ok(row.help, platform);
    assert.equal(i18n.t(row.help), 'Audio and subtitle languages are in Audio & subtitles.', platform);
  }
});
