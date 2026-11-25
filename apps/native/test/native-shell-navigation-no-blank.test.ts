import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// P2-71-H14b: a rail, tab or avatar navigation never fades the whole page area from 0. The chrome
// and page stay painted; only the route's own content changes, with its own motion.
test('H14b: navigation keeps the page painted, with no full-page opacity reset', () => {
  for (const file of ['../src-next/shell/TouchShell.tsx', '../src-next/shell/TVShell.tsx']) {
    const shell = readFileSync(new URL(file, import.meta.url), 'utf8');
    assert.doesNotMatch(shell, /pageOpacity/, `${file}: there is no page-wide opacity to reset`);
    const navigate = shell.slice(shell.indexOf('const navigate = ('), shell.indexOf('const openLibrary = ('));
    assert.doesNotMatch(navigate, /opacity|withTiming/, `${file}: navigate() animates nothing page-wide`);
  }
  const tvShell = readFileSync(new URL('../src-next/shell/TVShell.tsx', import.meta.url), 'utf8');
  assert.match(tvShell, /const pageStyle = useAnimatedStyle\(\(\) => \(\{transform: \[\{translateX: shift\.value\}\]\}\)\);/, 'the TV page keeps only the rail slide');
});
