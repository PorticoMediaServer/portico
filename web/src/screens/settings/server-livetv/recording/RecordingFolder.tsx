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
  if (!controller.isActive()) return null;
  return <section className={styles.section}>
    <SettingsRow label={t('web.liveTv.recordingFolder')} help={t('web.liveTv.recordingFolderHelp')}>
      <span style={{minWidth: 0, overflowWrap: 'anywhere'}}>{(controller.dirty()?state.draft:state.folder?.folder) || t('next.settings.loading')}</span>
      <Button ref={opener} size="small" variant="text" disabled={!state.document || state.loading || state.saving || state.uncertain} onClick={() => setOpen(true)}>{t('server.live.choose')}</Button>
    </SettingsRow>
    {state.loading && <p role="status">{t('status.loading')}</p>}
    {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error,'server-console',{operation:'action'})}</p>}
    {!!state.error && <Button size="small" variant="text" onClick={() => {if (controller.dirty()) void controller.save();else void controller.load();}}>{t('action.tryAgain')}</Button>}
    {state.folder?.state==='not_made'&&<p className={styles.lede}>{t('web.liveTv.recordingFolderNotMade')}</p>}
    {state.folder?.state==='unwritable'&&<p role="alert" className={styles.error}>{t('web.liveTv.notWritable')}{state.folder.problem&&` ${state.folder.problem}`}</p>}
    <ServerDirectoryExplorer open={open} onOpenChange={setOpen} api={api} onChoose={choose} initialPath={state.folder?.state==='ready'?(controller.dirty()?state.draft:state.folder.folder):undefined} checkWritable returnFocusRef={opener}/>
  </section>;
}
