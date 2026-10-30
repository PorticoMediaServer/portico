import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '../../../packages/client-core/src';
import {directoryAccessRefused} from '../src-next/screens/accounts/directory-refusal';

test('only typed server membership refusal selects the refused chooser', () => {
  assert.equal(directoryAccessRefused(new ApiError(403, 'access_refused', 'private body')), true);
  for (const error of [new ApiError(403, 'owner_required', ''), new ApiError(401, 'access_refused', ''),
    new ApiError(401, 'clock_skew', ''), new ApiError(503, 'server_unavailable', ''),
    Object.assign(new Error('retired'), {code: 'access_refused'}), new DOMException('cancel', 'AbortError'), undefined]) {
    assert.equal(directoryAccessRefused(error), false);
  }
});


test('refused presentation names the selected server and only offers another server or list refresh', async () => {
  const {screenModule} = await import('./helpers/screen-harness.mjs');
  const {defaultI18n} = await import('../../../packages/i18n/src');
  const react = {createElement: (type: unknown, props: unknown) => ({type, props})};
  const {DirectoryAccessRefused} = await screenModule(new URL('../src-next/screens/accounts/DirectoryAccessRefused.tsx', import.meta.url), {
    react: {default: react}, '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/states': {StateLayout: 'State'},
  });
  const events: string[] = [];
  const tree = DirectoryAccessRefused({serverName: 'Selected server', busy: false, onOther: () => events.push('other'), onRefresh: () => events.push('refresh')});
  assert.equal(tree.props.title, defaultI18n.t('web.chooser.refusedTitle', {server: 'Selected server'}));
  assert.equal(tree.props.body, defaultI18n.t('web.chooser.refusedBody'));
  assert.equal(tree.props.primaryAction.id, 'choose-another');
  assert.equal(tree.props.secondaryAction.id, 'refresh-servers');
  tree.props.primaryAction.onPress(); tree.props.secondaryAction.onPress();
  assert.deepEqual(events, ['other', 'refresh']);
  const busy = DirectoryAccessRefused({serverName: 'Selected server', busy: true, onOther() {}, onRefresh() {}});
  assert.equal(busy.props.primaryAction.disabled, true); assert.equal(busy.props.secondaryAction.disabled, true);
});
