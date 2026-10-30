import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';
import {TextField} from '../../kit/controls/Fields';
import {SettingsPanel} from '../../kit/settings';
import {SecureRefusalDialog} from './SecureRefusalDialog';
import {ownerSwitchContextAllowed} from '../../platform/secure-pending';
import type {SettingsOwnerSwitch} from './owner-switch';
import styles from './owner-signin-switch.module.css';

const t = defaultI18n.t;
/** This surface presents only the existing captured owner-move controller. */
export function OwnerSignInSwitch({source, isCurrent, onError, startOver = false}: {
  startOver?: boolean; source: SettingsOwnerSwitch; isCurrent: () => boolean; onError: (error: unknown) => void;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot);
  const [confirmation, setConfirmation] = useState('');
  const [open, setOpen] = useState(false), [secure, setSecure] = useState(false);
  const pending = useRef(false), alive = useRef(true);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const current = () => alive.current && isCurrent();
  const run = async (work: () => Promise<void>) => {
    if (!current() || pending.current) return;
    pending.current = true;
    try {await work();} catch (error) {if (current()) onError(error);} finally {pending.current = false;}
  };
  const start = () => {void run(async () => {
    // Refuse before preview/claim creation; the controller checks this again.
    if (!ownerSwitchContextAllowed()) {setSecure(true);return;}
    setConfirmation('');
    await source.preview(startOver);
    if (current()) setOpen(true);
  });};
  const close = () => {void run(async () => {
    if (state.busy || state.confirmed) return;
    await source.cancelPreview();
    if (current()) setOpen(false);
  });};
  const proceed = () => {void run(async () => {
    if (!state.preview || state.busy || state.preview.startOver !== startOver || startOver && confirmation !== state.preview.serverName) return;
    const url = state.confirmed ? await source.retryPreparation() : await source.confirmAndPrepare(startOver ? confirmation : '');
    if (current()) window.location.assign(url);
  });};
  const preview = state.preview;
  const busy = state.busy || pending.current;
  return <>
    <Button className={startOver ? styles.destructive : undefined} variant="glass" size="small" disabled={state.busy || !!state.preview || state.confirmed} onClick={start}>{t(startOver ? 'next.settingsSpec.server.signin.groups.1.rows.3.control.label' : 'next.settingsSpec.server.signin.groups.1.rows.2.control.label')}</Button>
    <SecureRefusalDialog open={secure} onOpenChange={value => {if (!value) setSecure(false);}} />
    <SettingsPanel open={open && !!preview} onClose={close} audience={t('next.shell.serverVisibility.ownerShared')}
      title={t(startOver ? 'next.settingsSpec.server.signin.panels.startOverPortico.title' : 'next.settingsSpec.server.signin.panels.switchToPortico.title')}
      dismissOnBack={!busy && !state.confirmed} showCloseButton={!busy && !state.confirmed}
      confirm={{label: t(startOver ? 'next.settingsSpec.server.signin.panels.startOverPortico.footer.primary' : 'next.settingsSpec.server.signin.panels.switchToPortico.footer.primary'),
        pending: busy, cancelDisabled: state.confirmed, destructive: startOver, disabled: busy || startOver && confirmation !== preview?.serverName, onConfirm: proceed}}>
      {preview && startOver ? <div className={styles.preview}>
        <p>{preview.serverName}</p>
        <h3>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.0.title')}</h3>
        <p>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.0.rows.0.help')}</p>
        <h3>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.title')}</h3>
        <dl>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.0.label')}</dt><dd>{defaultI18n.number(preview.people)}<p>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.0.help')}</p></dd>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.1.label')}</dt><dd>{preview.removedProfiles !== undefined && defaultI18n.number(preview.removedProfiles)}<p>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.1.help')}</p></dd>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.2.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.2.help')}</dd>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.3.label')}</dt><dd>{defaultI18n.number(preview.invitations)}</dd>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.4.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.4.help')}</dd>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.5.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.5.help')}</dd>
        </dl>
        <h3>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.2.title')}</h3>
        <dl><dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.0.label')}</dt><dd>{preview.retainedProfiles !== undefined && defaultI18n.number(preview.retainedProfiles)}<p>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.0.help')}</p></dd>
          <dt>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.1.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.1.help')}</dd></dl>
        <TextField label={t('next.settingsSpec.server.signin.panels.startOverPortico.groups.3.rows.0.label')} value={confirmation} disabled={busy || state.confirmed}
          onChange={event => setConfirmation(event.currentTarget.value)}/>
      </div> : preview && <div className={styles.preview}>
        <p>{preview.serverName}</p>
        <h3>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.0.title')}</h3>
        <p>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.0.rows.0.help')}</p>
        <h3>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.title')}</h3>
        <dl>
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.0.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.0.help')}</dd>
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.1.label')}</dt><dd>{defaultI18n.number(preview.people)}<p>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.1.help')}</p></dd>
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.2.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.2.help')}</dd>
          {preview.devices !== undefined && <><dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.3.label')}</dt><dd>{defaultI18n.number(preview.devices)}</dd></>}
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.4.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.4.help')}</dd>
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.5.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.5.help')}</dd>
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.6.label')}</dt><dd>{defaultI18n.number(preview.invitations)}<p>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.6.help')}</p></dd>
        </dl>
        <h3>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.2.title')}</h3>
        <dl><dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.2.rows.0.label')}</dt><dd/>
          <dt>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.2.rows.1.label')}</dt><dd>{defaultI18n.number(preview.profiles)}<p>{t('next.settingsSpec.server.signin.panels.switchToPortico.groups.2.rows.1.help')}</p></dd></dl>
      </div>}
    </SettingsPanel>
  </>;
}
