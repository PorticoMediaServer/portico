import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogue, createI18n, enUS, messageArguments, resolveRegion, supportedLocales} from '../../i18n/src/index.ts';

for (const id of ['next.shell.editorAudience.owner', 'next.shell.editorAudience.ownerPhone'] as const) {
  test(`${id} names the owner and administrators in every supported region`, () => {
    const expected = 'Only you and administrators see this';
    assert.equal(enUS[id], expected);
    assert.deepEqual(messageArguments(enUS[id]), []);
    for (const locale of supportedLocales) {
      const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
      assert.equal(i18n.t(id), expected, locale);
      if (locale !== 'en-US') assert.equal(catalogue(locale)[id], undefined, locale);
    }
  });
}

test('removed title copy keeps "any more" and substitutes title and server in every supported region', () => {
  const id = 'next.states.removed';
  assert.equal(enUS[id], "{title} isn't on {server} any more");
  assert.deepEqual(messageArguments(enUS[id]), ['server', 'title']);
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    assert.equal(i18n.t(id, {title: 'World Tonight', server: 'Summit'}), "World Tonight isn't on Summit any more", locale);
    if (locale !== 'en-US') assert.equal(catalogue(locale)[id], undefined, locale);
  }
});
