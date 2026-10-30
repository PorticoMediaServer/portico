import {useState, useSyncExternalStore} from 'react';
import {Switch} from '../../../kit/controls';
import {BackupPassphraseDialog} from '../../../kit/settings';
import type {BackupSurfaceProps} from '../server/Backups';

/** The switch follows the checked server policy; a passphrase stays inside its dialog. */
export function BackupProtection(props: BackupSurfaceProps & Readonly<{label: string}>) {
  const state = useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);
  const [open, setOpen] = useState(false);
  const protectedBackup = state.settings?.passphraseProtected === true;
  const unavailable = !state.settings || state.busy || !props.isCurrent();
  return <>
    <Switch aria-label={props.label} checked={protectedBackup} disabled={unavailable} onCheckedChange={checked => {
      if (unavailable || !props.isCurrent()) return;
      if (checked) setOpen(true);
      else void props.source.save({passphraseProtected: false}).then(saved => {
        if (!saved && props.isCurrent()) props.reportError(new Error('backup_protection_failed'));
      }).catch(() => {if (props.isCurrent()) props.reportError(new Error('backup_protection_failed'));});
    }} />
    <BackupPassphraseDialog open={open} mode="protect" scopeKey={props.scopeKey} isCurrent={props.isCurrent} subscribeAuthority={props.subscribe}
      onClose={() => setOpen(false)} onDone={() => setOpen(false)}
      onFailure={() => props.reportError(new Error('backup_protection_failed'))}
      submit={async (passphrase, signal) => {
        signal.throwIfAborted();
        if (!props.isCurrent()) throw new Error('backup_scope_ended');
        if (!await props.source.save({passphraseProtected: true, passphrase}, signal)) throw new Error('backup_protection_failed');
        signal.throwIfAborted();
        return 'done';
      }} />
  </>;
}
