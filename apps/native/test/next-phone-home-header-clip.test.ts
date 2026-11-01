import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

// Walk, 1 Nov: once Home scrolled, Android's BlurView behind the phone header dimmed the
// whole screen. The header clips its backdrop to its own band, as the tab bar does.
test('the phone Home header clips its blurred backdrop to the header', async () => {
  const source = await readFile(new URL('../src-next/shell/PhoneHomeHeader.tsx', import.meta.url), 'utf8');
  const container = source.match(/return <View pointerEvents="box-none" style=\{\{.*?zIndex/)?.[0] ?? '';
  assert.match(container, /height: geometry\.height/);
  assert.match(container, /overflow: 'hidden'/);
  assert.match(source, /scrolled && <BlurView/);
});
