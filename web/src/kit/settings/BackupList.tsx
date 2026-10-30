import {useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {BackupSummary} from '@core/administration.ts';
import {Button} from '../controls';
import {Icon} from '../Icon';
import {Menu} from '../overlays/Popover';
import styles from './settings.module.css';

export type BackupListProps = Readonly<{
  backups: readonly BackupSummary[];
  busy: boolean;
  scopeKey: string;
  isCurrent: () => boolean;
  subscribeAuthority: (listener: () => void) => () => void;
  /** Actual newest backup identity and the approved localized badge, supplied by its domain. */
  newest?: Readonly<{id: string; label: string}>;
  format: Pick<typeof defaultI18n, 'dateTime' | 'bytes' | 't'>;
  /** Open the real confirmation flows; these offers never execute a destructive mutation. */
  requestRestore: (backup: BackupSummary) => void;
  requestDelete: (backup: BackupSummary) => void;
}>;

/** Current Settings rows omit the retired file path, schema and server-version columns. */
export function BackupList(props: BackupListProps) {
  const current = useSyncExternalStore(props.subscribeAuthority, props.isCurrent, props.isCurrent);
  return current ? <BackupListRows key={props.scopeKey} {...props} /> : null;
}

function BackupListRows(props: BackupListProps) {
  return <div className={styles.backupList}>{props.backups.map(backup => <BackupListRow key={backup.id} {...props} backup={backup} />)}</div>;
}

function BackupListRow({backup, busy, isCurrent, newest, format, requestRestore, requestDelete}: BackupListProps & {backup: BackupSummary}) {
  const [open, setOpen] = useState(false), anchor = useRef<HTMLButtonElement>(null);
  const title = format.dateTime(backup.createdAt), kind = format.t(backup.kind === 'manual' ? 'web.maintenance.backupKindManual' : backup.kind === 'scheduled' ? 'web.maintenance.backupKindScheduled' : 'web.maintenance.backupKindPreRestore');
  const disabled = busy || !isCurrent();
  const run = (action: (backup: BackupSummary) => void) => {if (!busy && isCurrent()) action(backup);};
  return <div className={styles.backupListRow}>
    <Icon name="i-storage" size={20} />
    <div className={styles.backupListTitle}><b>{title}</b><small>{kind}, {format.bytes(backup.bytes)}</small></div>
    {newest?.id === backup.id && <span className={styles.backupNewest}>{newest.label}</span>}
    <Button className={styles.backupRestore} size="xs" variant="glass" disabled={disabled} onClick={() => run(requestRestore)}>{format.t('web.maintenance.restoreBackup')}</Button>
    <Button ref={anchor} size="xs" variant="icon" icon="i-more" iconOnly aria-label={format.t('next.home.moreFor', {title})} disabled={disabled} onClick={() => setOpen(value => !value)} />
    <Menu open={open && !disabled} onOpenChange={setOpen} anchorRef={anchor} title={title} returnFocusRef={anchor} items={[
      {id: 'restore', label: format.t('web.maintenance.restoreBackup'), onSelect: () => run(requestRestore)},
      {id: 'delete', icon: 'i-trash', destructive: true, label: format.t('web.maintenance.deleteBackupConfirm'), onSelect: () => run(requestDelete)},
    ]} />
  </div>;
}
