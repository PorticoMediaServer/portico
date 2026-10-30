import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsStructure} from '../src/presentation/settings-structure.ts';

test('audiobook jump choices remain independent profile settings on every platform', () => {
  for (const platform of ['web', 'phone', 'tv'] as const) {
    const structure = settingsStructure({platform, owner: false, capabilities: {}} as Parameters<typeof settingsStructure>[0]);
    const music = structure.headings.find(heading => heading.id === 'account')!.pages.find(page => page.id === 'music')!;
    assert.deepEqual(music.sections.slice(0, 3).map(section => section.id), ['sound', 'lyrics', 'audiobooks']);
    assert.equal(music.sections[0].title, 'next.settingsSpec.account.music.groups.0.title');
    const audiobooks = music.sections.find(section => section.id === 'audiobooks')!;
    assert.equal(audiobooks.title, 'next.settingsSpec.account.music.groups.2.title');
    const rows = audiobooks.rows.filter(row => row.kind === 'preference' && row.key !== 'audiobooks.defaultSpeed');
    const speedRows = structure.headings.flatMap(heading => heading.pages.flatMap(page => page.sections.flatMap(section => section.rows))).filter(row => row.kind === 'preference' && row.key === 'audiobooks.defaultSpeed');
    assert.equal(speedRows.length, 1);
    assert.ok(audiobooks.rows.includes(speedRows[0]));
    const expected = new Map([
      ['audiobooks.skipBackMs', [10000, 15000, 30000, 60000]],
      ['audiobooks.skipForwardMs', [15000, 30000, 60000, 90000]],
      ['audiobooks.resumeRewindMs', [0, 5000, 15000, 30000]],
    ]);
    for (const row of rows) {
      assert.equal(row.kind, 'preference');
      if (row.kind !== 'preference') continue;
      assert.equal(row.scope, 'profile');
      assert.equal(row.control.type, 'choice');
      if (row.control.type === 'choice') assert.deepEqual(row.control.choices.map(choice => choice.value), expected.get(row.key));
      expected.delete(row.key);
    }
    assert.equal(expected.size, 0);
  }
});
