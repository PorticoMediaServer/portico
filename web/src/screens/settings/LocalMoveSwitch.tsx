import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {AccountMove} from '@core/account-move.ts';
import {passwordStrength} from '@core/password-strength.ts';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';
import {Switch, TextField} from '../../kit/controls/Fields';
import {SettingsPanel} from '../../kit/settings';
import {SecureRefusalDialog} from './SecureRefusalDialog';
import {router} from '../../router';
import {cancelLocalMoveHandoff, offerLocalMove, type LocalMoveHandoff} from '../../platform/local-move-continuation';
import {ownerSwitchContextAllowed} from '../../platform/secure-pending';
import type {SettingsOwnerSwitch} from './owner-switch';
import styles from './owner-signin-switch.module.css';

const t = defaultI18n.t;
const panel = (startOver: boolean) => startOver
  ? 'next.settingsSpec.server.signin.panels.startOverLocal'
  : 'next.settingsSpec.server.signin.panels.switchToLocal';

/** One rendered opening, bound to the exact controller preview that produced it.
 * Its phase is the claim: only an `open` attempt can confirm, only the confirming
 * attempt can hand off, and it can never be revived after close or unmount. */
type Attempt = {
  readonly id: number;
  readonly preview: AccountMove;
  readonly startOver: boolean;
  readonly source: SettingsOwnerSwitch;
  /** Aborted synchronously when this exact opening closes or its layout unmounts. */
  readonly abort: AbortController;
  phase: 'open' | 'confirming' | 'handed' | 'closed';
};

type Draft = Readonly<{username: string; password: string; match: string; confirmation: string; twoStep: boolean}>;
type DraftState = Readonly<{revision: number; value: Draft}>;
const emptyDraft: Draft = Object.freeze({username: '', password: '', match: '', confirmation: '', twoStep: false});
const withField = <K extends keyof Draft>(draft: Draft, field: K, value: Draft[K]): Draft => Object.freeze({...draft, [field]: value});
const cancelPreview = (attempt: Attempt) => {
  try {if (attempt.source.getSnapshot().preview === attempt.preview) void attempt.source.cancelPreview().catch(() => {});} catch { /* the controller already ended */ }
};

/** The drawn Change group's move to local. The settings row opens the existing
 * owner-move preview; confirmation is the reviewed controller, and its one-shot
 * custody is paired with one private sign-in entry instance so that screen renders
 * its own challenge, profile and ready states. This surface never adopts or stores
 * a session and never carries a grant in a URL or storage. */
export function LocalMoveSwitch({source, isCurrent, onError, startOver = false}: {
  startOver?: boolean; source: SettingsOwnerSwitch; isCurrent: () => boolean; onError: (error: unknown) => void;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot);
  const [draft, setDraft] = useState<DraftState>({revision: 0, value: emptyDraft});
  const draftRef = useRef<DraftState>({revision: 0, value: emptyDraft});
  const [opening, setOpening] = useState<Attempt | undefined>(undefined), [secure, setSecure] = useState(false);
  const active = useRef<Attempt | undefined>(undefined), sourceRef = useRef(source), ticket = useRef<{retired: boolean} | undefined>(undefined);
  const sequence = useRef(0), starting = useRef(0);
  // A new controller replaces the old one; the old opening is retired as its own
  // instance. Layout cleanup also runs synchronously on unmount, before a late
  // continuation can publish for a successor.
  useLayoutEffect(() => {
    const owned = {retired: false};
    ticket.current = owned;sourceRef.current = source;
    return () => {
      owned.retired = true;starting.current = 0;
      const attempt = active.current;
      if (!attempt) return;
      active.current = undefined;
      const unconfirmed = attempt.phase === 'open';
      attempt.phase = 'closed';attempt.abort.abort();
      // A handed custody is not canceled here; its own admission owns it now.
      if (unconfirmed) cancelPreview(attempt);
    };
  }, [source]);
  const resetDraft = () => {
    const next: DraftState = {revision: draftRef.current.revision + 1, value: emptyDraft};
    draftRef.current = next;setDraft(next);
  };
  const editDraft = <K extends keyof Draft>(attempt: Attempt, field: K, value: Draft[K]) => {
    if (active.current !== attempt || attempt.phase !== 'open') return;
    const next: DraftState = {revision: draftRef.current.revision + 1, value: withField(draftRef.current.value, field, value)};
    draftRef.current = next;setDraft(next);
  };
  const start = () => {
    const owned = ticket.current, owner = source;
    if (!owned || owned.retired || owner !== sourceRef.current || starting.current || active.current) return;
    if (!isCurrent() || state.busy || state.preview || state.confirmed) return;
    // The captured controller checks this again; refuse before creating a preview.
    if (!ownerSwitchContextAllowed()) {setSecure(true);return;}
    const id = ++sequence.current;starting.current = id;
    resetDraft();
    void owner.previewDirection('local', startOver).then(preview => {
      if (owned.retired || owner !== sourceRef.current || id !== sequence.current) {
        // This start no longer owns the preview it created; retire exactly that one.
        try {if (owner.getSnapshot().preview === preview) void owner.cancelPreview().catch(() => {});} catch { /* the controller already ended */ }
        return;
      }
      resetDraft();
      const attempt: Attempt = {id, preview, startOver, source: owner, abort: new AbortController(), phase: 'open'};
      active.current = attempt;setOpening(attempt);
    }).catch(error => {
      if (!owned.retired && owner === sourceRef.current && id === sequence.current) onError(error);
    }).finally(() => {if (starting.current === id) starting.current = 0;});
  };
  const close = (attempt: Attempt) => {
    if (active.current !== attempt || attempt.phase !== 'open') return;
    attempt.phase = 'closed';attempt.abort.abort();active.current = undefined;setOpening(undefined);resetDraft();cancelPreview(attempt);
  };
  const proceed = (attempt: Attempt, renderedRevision: number) => {
    if (active.current !== attempt || attempt.phase !== 'open') return;
    // The button is bound to the draft tuple this render drew; any later edit
    // increments the revision, so a retained Confirm cannot submit older input.
    if (draftRef.current.revision !== renderedRevision) return;
    const preview = attempt.preview, values = draftRef.current.value;
    if (preview.to !== 'local' || preview.startOver !== attempt.startOver) return;
    if (!values.username.trim() || !values.password || values.password !== values.match || !passwordStrength(values.password).acceptable) return;
    if (attempt.startOver && values.confirmation !== preview.serverName) return;
    if (attempt.source.getSnapshot().preview !== preview) return;
    const owned = ticket.current;
    if (!owned || owned.retired || ticket.current !== owned || attempt.source !== sourceRef.current) return;
    // Claim this exact opening and freeze only its admitted tuple before any await.
    const submission = Object.freeze({username: values.username.trim(), password: values.password, confirmation: attempt.startOver ? values.confirmation : '', twoStep: values.twoStep});
    attempt.phase = 'confirming';resetDraft();
    void (async () => {
      try {
        const custody = await attempt.source.confirmLocal({username: submission.username, password: submission.password}, submission.confirmation, {
          // Joined caller liveness: the captured opening and its ticket, plus the
          // synchronously aborted signal, stop credentials before any IO.
          isCurrent: () => active.current === attempt && attempt.phase === 'confirming' && ticket.current === owned && !owned.retired,
          signal: attempt.abort.signal,
        });
        // Compare the originally captured ticket; a replaced layout cannot revive it.
        if (active.current !== attempt || attempt.phase !== 'confirming' || ticket.current !== owned || owned.retired || sourceRef.current !== attempt.source) {
          attempt.phase = 'closed';attempt.abort.abort();await custody.retire();return;
        }
        let handoff: LocalMoveHandoff;
        // Only the two-step choice goes on; the password stays with this form.
        try {handoff = offerLocalMove(custody, {twoStep: submission.twoStep});}
        catch (error) {attempt.phase = 'closed';throw error;}
        attempt.phase = 'handed';
        try {await router.navigate({to: '/sign-in'});}
        catch (error) {cancelLocalMoveHandoff(handoff);throw error;}
      } catch (error) {
        attempt.phase = 'closed';attempt.abort.abort();
        if (active.current === attempt) active.current = undefined;
        if (ticket.current === owned && !owned.retired) onError(error);
      }
    })();
  };
  const current = opening && active.current === opening ? opening : undefined;
  const preview = current?.preview, busy = state.busy || current?.phase === 'confirming' || current?.phase === 'handed';
  const mismatch = Boolean(draft.value.match) && draft.value.password !== draft.value.match, keys = panel(startOver);
  const ready = Boolean(current && current.phase === 'open' && preview && preview.to === 'local' && preview.startOver === startOver
    && draft.value.username.trim() && draft.value.password && !mismatch && (!startOver || draft.value.confirmation === preview.serverName)
    && passwordStrength(draft.value.password).acceptable);
  return <>
    <Button className={startOver ? styles.destructive : undefined} variant="glass" size="small" disabled={state.busy || !!state.preview || state.confirmed || !!active.current}
      onClick={start}>{t(`${startOver ? 'next.settingsSpec.server.signin.groups.1.rows.1' : 'next.settingsSpec.server.signin.groups.1.rows.0'}.control.label`)}</Button>
    <SecureRefusalDialog open={secure} onOpenChange={value => {if (!value) setSecure(false);}} />
    <SettingsPanel open={!!current && !!preview && state.preview === preview} onClose={() => {if (current) close(current);}}
      audience={t('next.shell.serverVisibility.ownerShared')} title={t(`${keys}.title`)} dismissOnBack={!busy} showCloseButton={!busy}
      confirm={{label: t(`${keys}.footer.primary`), pending: busy, disabled: busy || !ready, destructive: startOver,
        onConfirm: () => {if (current) proceed(current, draft.revision);}}}>
      {preview && <div className={styles.preview}>
        <p>{preview.serverName}</p>
        {startOver ? <>
          <h3>{t(`${keys}.groups.0.title`)}</h3>
          <dl>
            <dt>{t(`${keys}.groups.0.rows.0.label`)}</dt><dd>{defaultI18n.number(preview.people)}<p>{t(`${keys}.groups.0.rows.0.help`)}</p></dd>
            <dt>{t(`${keys}.groups.0.rows.1.label`)}</dt><dd>{preview.removedProfiles !== undefined && defaultI18n.number(preview.removedProfiles)}<p>{t('next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.1.help')}</p></dd>
            <dt>{t(`${keys}.groups.0.rows.2.label`)}</dt><dd>{t(`${keys}.groups.0.rows.2.help`)}</dd>
            <dt>{t(`${keys}.groups.0.rows.3.label`)}</dt><dd>{defaultI18n.number(preview.invitations)}</dd>
            <dt>{t(`${keys}.groups.0.rows.4.label`)}</dt><dd>{t(`${keys}.groups.0.rows.4.help`)}</dd>
            <dt>{t(`${keys}.groups.0.rows.5.label`)}</dt><dd>{t(`${keys}.groups.0.rows.5.help`)}</dd>
          </dl>
          <h3>{t(`${keys}.groups.1.title`)}</h3>
          <dl>
            <dt>{t(`${keys}.groups.1.rows.0.label`)}</dt><dd>{preview.retainedProfiles !== undefined && defaultI18n.number(preview.retainedProfiles)}<p>{t('next.settingsSpec.server.signin.panels.startOverLocal.groups.1.rows.0.help')}</p></dd>
            <dt>{t(`${keys}.groups.1.rows.1.label`)}</dt><dd>{t(`${keys}.groups.1.rows.1.help`)}</dd>
          </dl>
        </> : <>
          <h3>{t(`${keys}.groups.0.title`)}</h3>
          <dl>
            <dt>{t(`${keys}.groups.0.rows.0.label`)}</dt><dd>{defaultI18n.number(preview.people)}<p>{t(`${keys}.groups.0.rows.0.help`)}</p></dd>
            <dt>{t(`${keys}.groups.0.rows.1.label`)}</dt><dd>{preview.devices !== undefined && defaultI18n.number(preview.devices)}</dd>
            <dt>{t(`${keys}.groups.0.rows.2.label`)}</dt><dd>{t(`${keys}.groups.0.rows.2.help`)}</dd>
            <dt>{t(`${keys}.groups.0.rows.3.label`)}</dt><dd>{t('next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.3.help')}</dd>
            <dt>{t(`${keys}.groups.0.rows.4.label`)}</dt><dd>{t(`${keys}.groups.0.rows.4.help`)}</dd>
            <dt>{t(`${keys}.groups.0.rows.5.label`)}</dt><dd>{defaultI18n.number(preview.invitations)}<p>{t(`${keys}.groups.0.rows.5.help`)}</p></dd>
            <dt>{t('next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.6.label')}</dt><dd>{t('next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.6.help')}</dd>
          </dl>
          <h3>{t(`${keys}.groups.1.title`)}</h3>
          <dl>
            <dt>{t(`${keys}.groups.1.rows.0.label`)}</dt><dd/>
            <dt>{t(`${keys}.groups.1.rows.1.label`)}</dt><dd>{defaultI18n.number(preview.profiles)}<p>{t(`${keys}.groups.1.rows.1.help`)}</p></dd>
          </dl>
        </>}
        <h3>{t(`${keys}.groups.2.title`)}</h3>
        <TextField label={t(`${keys}.groups.2.rows.0.label`)} description={t(`${keys}.groups.2.rows.0.help`)}
          placeholder={t(`${keys}.groups.2.rows.0.control.placeholder`)} autoComplete="username" value={draft.value.username} disabled={busy}
          onChange={event => {if (current) editDraft(current, 'username', event.currentTarget.value);}}/>
        <TextField type="password" label={t(`${keys}.groups.2.rows.1.label`)}
          placeholder={t(`${keys}.groups.2.rows.1.control.placeholder`)} autoComplete="new-password" value={draft.value.password} disabled={busy}
          onChange={event => {if (current) editDraft(current, 'password', event.currentTarget.value);}}/>
        <TextField type="password" label={t(`${keys}.groups.2.rows.2.label`)} error={mismatch ? t('web.reset.mismatch') : undefined}
          placeholder={t(`${keys}.groups.2.rows.2.control.placeholder`)} autoComplete="new-password" value={draft.value.match} disabled={busy}
          onChange={event => {if (current) editDraft(current, 'match', event.currentTarget.value);}}/>
        <label className={styles.toggle}><span>{t(`${keys}.groups.2.rows.3.label`)}<small>{t(`${keys}.groups.2.rows.3.help`)}</small></span>
          <Switch checked={draft.value.twoStep} disabled={busy} onCheckedChange={value => {if (current) editDraft(current, 'twoStep', value);}}/></label>
        {startOver && <>
          <h3>{t('next.settingsSpec.server.signin.panels.startOverLocal.groups.3.title')}</h3>
          <TextField label={t('next.settingsSpec.server.signin.panels.startOverLocal.groups.3.rows.0.label')} placeholder={t('next.settingsSpec.server.signin.panels.startOverLocal.groups.3.rows.0.control.placeholder')}
            value={draft.value.confirmation} disabled={busy} onChange={event => {if (current) editDraft(current, 'confirmation', event.currentTarget.value);}}/>
        </>}
      </div>}
    </SettingsPanel>
  </>;
}
