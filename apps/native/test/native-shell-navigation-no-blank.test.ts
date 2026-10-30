import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// P2-71-H14b: a rail, tab or avatar navigation never fades the whole page area from 0. The chrome
// and page stay painted; only the route's own content changes, with its own motion.
test('H14b: navigation keeps the page painted, with no full-page opacity reset', () => {
  const shell = readFileSync(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(shell, /pageOpacity/, 'there is no page-wide opacity to reset');
  const navigate = shell.slice(shell.indexOf('const navigate = ('), shell.indexOf('const openLibrary = ('));
  assert.doesNotMatch(navigate, /opacity|withTiming/, 'navigate() animates nothing page-wide');
  assert.match(shell, /const pageStyle = useAnimatedStyle\(\(\) => \(\{transform: \[\{translateX: shift\.value\}\]\}\)\);/, 'the page keeps only the account slide');
});
