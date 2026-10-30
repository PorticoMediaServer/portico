import {useEffect} from 'react';
import {createPortal} from 'react-dom';
import {defaultI18n} from '@i18n';
import {Toast} from '../../kit/overlays';
import type {PlaylistRemovalUndo} from './removal.ts';
import {savedText as s} from './text.ts';
import styles from './saved.module.css';

/** Unlike an ordinary timed toast, this action lasts exactly as long as the
 * committed server token. The server still makes the final expiry decision. */
export function RemovalUndo({value, clear, undo}: {value?: PlaylistRemovalUndo; clear: () => void; undo: (value: PlaylistRemovalUndo) => void}) {
  useEffect(() => {
    if (!value) return;
    let timer: ReturnType<typeof setTimeout>;
    const expire = () => {
      const remaining = value.until - Date.now();
      if (remaining <= 0) clear();
      else timer = setTimeout(expire, Math.min(remaining, 2_147_483_647));
    };
    expire();
    return () => clearTimeout(timer);
  }, [value, clear]);
  return value && typeof document !== 'undefined' ? createPortal(<div className={styles.removalToast}>
    <Toast open onOpenChange={open => { if (!open) clear(); }} message={s('next.saved.removedPlaylist', {title: value.title})}
      action={{label: defaultI18n.t('next.overlays.undo'), variant: 'text', onAction: () => { if (Date.now() < value.until) undo(value); }}} />
  </div>, document.body) : null;
}
