import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const css = readFileSync(new URL('../src/screens/dashboard/dashboard-quiet.module.css', import.meta.url), 'utf8');
const row = (selector: string, embedded = false) => Number(css.match(new RegExp(`\\.grouped${embedded ? '\\[data-quiet-embedded\\]' : ''} > \\[${selector}\\] \\{[^}]*grid-row: (\\d+);`))?.[1]);

test('the desktop Dashboard runs in settings.html\'s order: Alerts, Now playing, Server load, the statistics, Background tasks last (lead, 4 Nov)', () => {
  for (const embedded of [false, true]) {
    const order = ["data-dashboard-section='updates'", "data-dashboard-section='streams'", "data-dashboard-quiet-slot='load'", "data-dashboard-section='activity'", "data-dashboard-quiet-slot='tasks'"].map(selector => row(selector, embedded));
    assert.ok(order.every(Number.isFinite), JSON.stringify(order));
    assert.deepEqual([...order].sort((a, b) => a - b), order, `${embedded ? 'embedded' : 'page'}: ${JSON.stringify(order)}`);
    assert.equal(new Set(order).size, order.length, 'one section per row, none side by side');
  }
  // Alerts and Background tasks run the width of the page, as one box each.
  assert.match(css, /\.grouped > \[data-dashboard-section='updates'\] \{ grid-column: 1 \/ -1;/);
  assert.match(css, /\.grouped > \[data-dashboard-quiet-slot='tasks'\] \{ grid-column: 1 \/ -1;/);
});

test('a quiet desktop Dashboard still heads its last section "Background tasks"', () => {
  const page = readFileSync(new URL('../src/screens/dashboard/Dashboard.tsx', import.meta.url), 'utf8');
  assert.match(page, /: <Section title=\{t\('next\.dashboard\.tasks'\)\} extra=\{<DashboardQuiet kind="tasks" \/>\}>/);
});
