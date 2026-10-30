import {useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './profile.module.css';
const t = defaultI18n.t;

/** The shell owns credential retirement, selected-server cleanup and navigation. */
export function ProfileSignOut({serverName, isCurrent, signOut}: {serverName: string; isCurrent: () => boolean; signOut: () => void}) {
  const [open, setOpen] = useState(false), trigger = useRef<HTMLButtonElement>(null), submitted = useRef(false);
  if (!isCurrent()) return null;
  const confirm = () => {
    if (!isCurrent() || submitted.current) return;
    submitted.current = true;setOpen(false);signOut();
  };
  return <>
    <Button ref={trigger} size="small" variant="glass" className={styles.danger} onClick={() => {if (isCurrent()) setOpen(true);}}>{t('accounts.signOut.server')}</Button>
    <Dialog children={null} open={open} returnFocusRef={trigger} size="sm" title={t('accounts.signOut.namedServer', {server: serverName})}
      description={t('accounts.signOut.serverConfirmation', {server: serverName})}
      onOpenChange={value => {if (isCurrent() && !submitted.current) setOpen(value);}}
      footer={<><Button size="small" onClick={() => setOpen(false)}>{t('action.cancel')}</Button><Button size="small" variant="glass" className={styles.danger} onClick={confirm}>{t('accounts.signOut.server')}</Button></>} />
  </>;
}
