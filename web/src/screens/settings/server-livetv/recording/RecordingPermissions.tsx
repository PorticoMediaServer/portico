import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {recordingOwnerKey, type RecordingOwnerChoice} from '@core/server-admin/recording-policy.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Switch} from '../../../../kit/controls';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import type {RecordingPermissionsController} from './permissions-controller';
import styles from '../../../live/owner/owner.module.css';
const t = defaultI18n.t;
/** Who can record: one Can record switch per current profile, the same switch as
 * Recordings in that profile's settings. Nothing is inherited from the account. */
export function RecordingPermissions({controller, embedded = false}: {embedded?: boolean; controller: RecordingPermissionsController}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  useEffect(() => {void controller.load();}, [controller]);
  if (!controller.isActive()) return null;
  const rows: RecordingOwnerChoice[] = [...state.owners];
  const allowed = (row: RecordingOwnerChoice) => state.edits[recordingOwnerKey(row.owner)]?.enabled ?? row.canRecord;
  const dirty = Object.keys(state.edits).length > 0;
  const groups = new Map<string, {name: string; profiles: RecordingOwnerChoice[]}>();
  for (const row of rows) {const key = JSON.stringify([row.owner.authority, row.owner.accountId]), group = groups.get(key) ?? {name: row.accountName, profiles: []};group.profiles.push(row);groups.set(key, group);}
  return <section className={styles.section}><div className={styles.heading}><h3>{t('web.dvrSettings.whoCanRecord')}</h3>
    <Button variant="icon" icon="i-refresh" iconOnly size="small" aria-label={t('action.refresh')} disabled={state.loading || state.saving || dirty || state.uncertain} onClick={() => void controller.load()}/></div>
    <p className={styles.lede}>{t('web.dvrSettings.whoCanRecordLede')}</p>
    {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}{dirty && <Button size="small" disabled={state.saving} onClick={() => void controller.save()}>{t('action.tryAgain')}</Button>}</p>}
    {state.loading && !rows.length && <p role="status">{t('status.loading')}</p>}
    {[...groups].map(([key, group]) => <section key={key} className={styles.sources}><h4 className={styles.accountHeading}>{group.name}</h4>{group.profiles.map(row => {
      const key = recordingOwnerKey(row.owner), on = allowed(row);
      const disabled = state.loading || state.saving || !!state.uncertain;
      return <div key={key}>
        <SettingsRow label={row.profileName} help={t(on ? 'next.liveOwner.canRecord' : 'next.liveOwner.cannotRecord')}>
          <Switch checked={on} disabled={disabled} aria-label={t('web.dvrSettings.allowRecording') + ': ' + row.profileName} onCheckedChange={enabled => controller.edit(row.owner, {enabled})}/>
        </SettingsRow>
      </div>;
    })}</section>)}
    {state.cursor && <Button size="small" loading={state.loading} disabled={state.saving || dirty || state.uncertain} onClick={() => void controller.load(true)}>{t('action.showMore')}</Button>}
    {!embedded && dirty && <div className={styles.save}><Button variant="text" size="small" disabled={state.saving || state.uncertain} onClick={() => controller.discard()}>{t('action.discard')}</Button><Button variant="primary" size="small" loading={state.saving} onClick={() => void controller.save()}>{t('action.save')}</Button></div>}
  </section>;
}
