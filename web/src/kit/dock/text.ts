import {formatMessage} from '@i18n';
import {dockMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dock.ts';
import type {DockTranslate, DockView} from './types';

export const dockText: DockTranslate = (id, values) => formatMessage(dockMessages[id], values);
export function remoteText(view: DockView, t: DockTranslate): string | undefined {
  const remote = view.remote;
  if (!remote) return undefined;
  switch (remote.phase) {
    case 'connecting': return t('next.dock.chooseDevice');
    case 'pairing': return t('next.dock.connecting', {device: view.presentation.deviceName ?? ''});
    case 'waiting': return t('next.dock.waiting');
    case 'playing': return t('next.dock.onDevice', {device: view.presentation.deviceName ?? ''});
    case 'stopped': return t('next.dock.stopped');
    case 'replaced': return t('next.dock.replaced');
    case 'error': return view.presentation.remoteError ?? t('next.dock.remoteError');
  }
}
