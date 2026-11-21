import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const rules = read('../src/screens/live/SeriesRules.tsx'), screen = read('../src/screens/live/RecordingsScreen.tsx');

// Recordings and Series rules are drawn on the Live TV frame (kit/frames LiveFrame) through one RecordingsFrame.
test('on a phone Recordings is phone-app 12\'s sub-page: "‹ Live TV" and its title, in place of the Live TV head and its tabs', () => {
  assert.match(rules, /const phone = usePhone\(\), sub = phone && !!onGuide;/);
  assert.match(rules, /<LiveFrame title=\{sub \? t\('recordings\.title'\) : t\('next\.home\.shell\.live'\)\} back=\{sub \? \{label: t\('next\.home\.shell\.live'\), onPress: onGuide!\} : undefined\}/);
  for (const [name, source, guide] of [['RecordingsScreen', screen, 'goGuide'], ['SeriesRules', rules, 'onGuide']] as const)
    assert.match(source, new RegExp(`<RecordingsFrame [^>]*onGuide=\\{${guide}\\}`), name);
});

test('the phone views are frame 12\'s five underline tabs (Storage among them); wider, Guide | Recordings tabs and the views as chips', () => {
  assert.match(rules, /const tabs = phone \? <Tabs phone value=\{view\}[^]*items=\{views\.map\(item => \(\{value: item\.id, label: item\.label\}\)\)\}\/>/);
  assert.doesNotMatch(rules, /item\.id !== 'storage'/);
  assert.match(rules, /chips=\{!phone && <>\{views\.map\(item => <Chip /);
  assert.match(screen, /\{id: 'storage' as const, label: t\('recordings\.storage'\)\}/);
});
