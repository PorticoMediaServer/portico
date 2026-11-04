import assert from 'node:assert/strict';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {OwnerEditorTag} from '../src/kit/selection/OwnerEditorTag.tsx';
import {NotificationsPanelState} from '../src/kit/shell/NotificationsPanel.tsx';

test('the owner\'s bulk editor sits under the Server mark and its audience line (select.html frames 8 to 11)', () => {
  const html = renderToStaticMarkup(createElement(OwnerEditorTag));
  assert.match(html, /data-authority="owner"/);
  assert.match(html, /i-server/);
  assert.match(html, />Server</);
  assert.match(html, /Only you and administrators see this/);
  assert.doesNotMatch(html, /Owner only/);
});

test('an empty or failed inbox sits inside the panel\'s inset, not against its edge', () => {
  const html = renderToStaticMarkup(createElement(NotificationsPanelState, null, createElement('p', null, 'state')));
  assert.match(html, /^<div class="[^"]*inboxState[^"]*"><p>state<\/p><\/div>$/);
});
