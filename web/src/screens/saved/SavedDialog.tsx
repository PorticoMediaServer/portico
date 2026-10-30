import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import {Dialog, type DialogProps} from '../../kit/overlays';
import styles from './saved.module.css';

/** collections.html compact Saved dialogs. Shared modal focus, dismissal and
 * motion remain canonical; only the drawn Saved geometry/glyph differ. */
export function SavedDialog({width = 372, className, children, busy, ...props}: Omit<DialogProps, 'showCloseButton'> & {busy?: boolean}) {
  return <Dialog {...props} width={width} className={[styles.compactDialog, className].filter(Boolean).join(' ')} showCloseButton={false}>
    <Button variant="icon" size="small" iconOnly className={styles.compactDialogClose} aria-label={defaultI18n.t('action.close')} disabled={busy} onClick={() => props.onOpenChange(false, 'close-button')}>
      <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" d="M6 6l12 12M18 6 6 18" /></svg>
    </Button>
    {children}
  </Dialog>;
}
