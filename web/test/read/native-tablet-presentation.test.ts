import assert from 'node:assert/strict';
import {test} from 'node:test';
import {defaultI18n} from '@i18n';
import {readingDisplayCopy} from '../../src/screens/read/display-copy.ts';
import {readerBookPresentation} from '../../src/screens/read/native/tablet-presentation.ts';

test('expanded native tablet keeps touch controls and the landscape two-pages setting', () => {
  const layout = readerBookPresentation(1180, 'native');
  assert.equal(layout.showPageArrows, false);
  assert.equal(layout.fullscreen, false);
  const copy = readingDisplayCopy(defaultI18n, layout.presentation, 7);
  assert.equal(copy.twoPages?.label, 'Two pages side by side in landscape');
  assert.equal(copy.columns, undefined);
  assert.equal(layout.displayPopover, true);
  assert.equal(layout.panelReflows('display'), false, 'Aa overlays the spread rather than shrinking it');
  assert.equal(layout.panelReflows('contents'), true);
  assert.equal(layout.panelReflows('search'), true);
  assert.equal(layout.panelReflows(undefined), false);
});

test('resizing a native tablet through medium and compact selects the drawn panel surfaces', () => {
  for (const width of [600, 626, 744, 820, 1023]) {
    const layout = readerBookPresentation(width, 'native');
    assert.equal(layout.presentation, 'tablet');
    assert.equal(layout.displayPopover, false);
    assert.equal(layout.panelReflows('display'), true);
    assert.equal(layout.fullscreen, false);
    assert.equal(readingDisplayCopy(defaultI18n, layout.presentation, 7).twoPages !== undefined, true);
  }
  const compact = readerBookPresentation(599, 'native');
  assert.equal(compact.presentation, 'phone');
  assert.equal(compact.panelReflows('display'), false);
  assert.equal(compact.panelReflows('contents'), false);
  assert.equal(compact.displayPopover, false);
  assert.equal(readingDisplayCopy(defaultI18n, compact.presentation, 7).twoPages, undefined);
  assert.equal(readerBookPresentation(1024, 'native').displayPopover, true);
});

test('web default retains desktop controls and existing responsive behavior', () => {
  for (const width of [390, 599, 600, 820, 1023, 1024, 1180, 1920]) {
    const layout = readerBookPresentation(width);
    assert.equal(layout.presentation, width < 600 ? 'phone' : width < 1024 ? 'tablet' : 'desktop');
    assert.equal(layout.showPageArrows, width >= 1024);
    assert.equal(layout.fullscreen, width >= 1024);
    assert.equal(layout.panelReflows('display'), width >= 600);
    assert.equal(layout.displayPopover, false);
  }
});
