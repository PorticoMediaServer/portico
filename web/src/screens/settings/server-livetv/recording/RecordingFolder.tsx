import {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button} from '../../../../kit/controls';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import {ServerDirectoryExplorer} from '../../../../kit/settings/ServerDirectoryExplorer';
import type {RecordingRootController} from './root-controller';
import styles from '../../../live/owner/owner.module.css';
const t = defaultI18n.t;
export function RecordingFolder({controller, api}: {controller: RecordingRootController; api: LibraryContentApi}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const [open, setOpen] = useState(false), opener = useRef<HTMLButtonElement>(null);
  useEffect(() => {void controller.load();}, [controller]);
  const choose = useCallback((path: string) => {if (controller.isActive()) {controller.choose(path);setOpen(false);void controller.save();}}, [controller]);
  if (!controller.isActive() || state.unsupported) return null;
  return <section className={styles.section}>
    <SettingsRow label={t('web.liveTv.recordingFolder')} help={t('web.liveTv.recordingFolderHelp')}>
      <span style={{minWidth: 0, overflowWrap: 'anywhere'}}>{state.draft || t('web.liveTv.recordingFolderUnset')}</span>
      <Button ref={opener} size="small" variant="text" disabled={!state.status || state.loading || state.saving || state.uncertain || state.status.operatorControlled} onClick={() => setOpen(true)}>{t('server.live.choose')}</Button>
    </SettingsRow>
    {state.terminalRequest && <p role="status">{t('web.liveTv.recordingFolderRequestRetired')}</p>}
    {state.loading && <p role="status">{t('status.loading')}</p>}
    {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
    {!!state.error && <Button size="small" variant="text" onClick={() => {if (controller.dirty()) void controller.save();else void controller.load(true);}}>{t('action.tryAgain')}</Button>}
    {state.status && <p className={styles.lede} style={{overflowWrap: 'anywhere'}}>{t('web.liveTv.recordingFolderEffective', {path: state.status.effective || t('web.liveTv.recordingFolderUnset')})}</p>}
    {state.status?.pendingRestart && <p role="status">{t('web.liveTv.recordingFolderPending')}</p>}
    {state.status?.operatorControlled && <p className={styles.lede}>{t('web.liveTv.recordingFolderOperator')}</p>}
    <ServerDirectoryExplorer open={open} onOpenChange={setOpen} api={api} onChoose={choose} initialPath={state.draft} checkWritable returnFocusRef={opener}/>
  </section>;
}
