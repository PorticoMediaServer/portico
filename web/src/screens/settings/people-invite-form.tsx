import {defaultI18n} from '@i18n';
import {RichSelect, Segmented, Switch, TextField} from '../../kit/controls';
import {useState} from 'react';
import {SettingsAdvanced, SettingsPanel} from '../../kit/settings';
import styles from './people-invite.module.css';
import type {InviteAttempt, InviteChoices} from './people-invite';
import {expiryChoices, inviteChoicesOf, ratingChoices, streamChoices} from './people-invite';

const t = defaultI18n.t;
const spec = 'next.settingsSpec.server.people.panels.invite.';

/** The drawn Invite someone form (owner-people frames 2/23b/11, server-people
 * invite panel): role, libraries, streams at once, away from home, expiry,
 * optional Portico Account username, private notes, and in Advanced access
 * tokens and highest rating. It writes nothing
 * until Create. While an uncertain create is being reconciled it shows that
 * attempt's original choices with Retry or Dismiss, so an edited form can never
 * submit an old body. */
export function PeopleInviteForm({open, onOpenChange, choices, onChoices, libraries, signIn, audience, busy, error, retained, onCreate, onRetry}: Readonly<{
  open: boolean; onOpenChange: (open: boolean) => void; choices: InviteChoices; onChoices: (choices: InviteChoices) => void;
  libraries: readonly {id: string; name: string}[]; signIn?: 'local' | 'portico'; audience?: string;
  busy: boolean; error: unknown; retained?: InviteAttempt;
  onCreate: () => void; onRetry: () => void;
}>) {
  const locked = !!retained && !busy;
  const shown: InviteChoices = retained ? inviteChoicesOf(retained.body) : choices;
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const ratingIndex = Math.max(0, ratingChoices.indexOf(shown.maxRating as typeof ratingChoices[number]));
  // The collapsed Advanced section names what it holds that differs from a new member's defaults.
  const advancedSummary = [
    ...(!shown.accessTokens ? [`${t(`${spec}advanced.groups.0.rows.0.label`)}: ${t('next.settings.off')}`] : []),
    ...(shown.maxRating ? [`${t(`${spec}advanced.groups.0.rows.1.label`)}: ${t(`${spec}advanced.groups.0.rows.1.control.options.${ratingIndex}.label` as never)}`] : []),
  ];
  const allChecked = shown.allLibraries;
  const set = (patch: Partial<InviteChoices>) => onChoices({...choices, ...patch});
  return <SettingsPanel open={open} onClose={() => {if (!busy) onOpenChange(false);}} title={t('next.settings.people.inviteTitle')}
    audience={audience ?? t('next.shell.serverVisibility.ownerShared')} dismissOnBack={!busy} showCloseButton={!busy}
    create={{label: t(retained ? 'next.settings.people.retryCreate' : 'next.settings.people.create'),
      cancelLabel: t(retained ? 'next.settings.people.dismiss' : 'next.settings.cancel'), pending: busy,
      disabled: busy || (!retained && !shown.allLibraries && !shown.libraryIds.length), onCreate: retained ? onRetry : onCreate}}>
    <div className={styles.form}>
      {retained && <p role="alert">{t('next.settings.people.unsureCreate')}</p>}
      {!retained && error ? <p role="alert">{t('next.settings.people.createFailed')}</p> : null}
      <fieldset disabled={locked || busy} className={styles.fieldset}>
        <legend>{t('next.settings.people.role')}</legend>
        {(['member', 'admin'] as const).map(role => <label key={role} className={styles.choice}>
          <input type="radio" name="invite-role" checked={shown.role === role} onChange={() => set({role})} />
          <span>{t(role === 'admin' ? 'next.settings.people.adminRole' : 'next.settings.people.memberRole')}</span>
        </label>)}
      </fieldset>
      <fieldset disabled={locked || busy} className={styles.fieldset}>
        <legend>{t('next.settings.people.libraries')}</legend>
        <label className={styles.choice}>
          <input type="checkbox" checked={allChecked} onChange={event => {
            set({allLibraries: event.currentTarget.checked, ...(event.currentTarget.checked ? {libraryIds: []} : {})});
          }} />
          <span>{t('next.settings.people.allLibraries')}</span>
        </label>
        {!allChecked && libraries.map(library => <label key={library.id} className={styles.choice}>
          <input type="checkbox" checked={shown.libraryIds.includes(library.id)} onChange={event => {
            const next = event.currentTarget.checked ? [...shown.libraryIds, library.id] : shown.libraryIds.filter(id => id !== library.id);
            set({allLibraries: false, libraryIds: next});
          }} />
          <span>{library.name}</span>
        </label>)}
      </fieldset>
      <RichSelect label={t(`${spec}groups.0.rows.4.label`)} description={t(`${spec}groups.0.rows.4.help`)} value={shown.streams} disabled={locked || busy}
        options={streamChoices.map((value, index) => ({value, label: t(`${spec}groups.0.rows.4.control.options.${index}.label` as never),
          ...(index === 0 ? {description: t(`${spec}groups.0.rows.4.control.options.0.hint`)} : {})}))}
        onValueChange={value => set({streams: value as InviteChoices['streams']})} />
      <fieldset disabled={locked || busy} className={styles.fieldset}>
        <legend>{t(`${spec}groups.0.rows.5.label`)}</legend>
        <Segmented aria-label={t(`${spec}groups.0.rows.5.label`)} value={shown.awayAllowed ? 'allowed' : 'home'} disabled={locked || busy}
          items={[{value: 'allowed', label: t(`${spec}groups.0.rows.5.control.options.0.label`)}, {value: 'home', label: t(`${spec}groups.0.rows.5.control.options.1.label`)}]}
          onValueChange={value => set({awayAllowed: value === 'allowed'})} />
      </fieldset>
      <RichSelect label={t('next.settings.people.expiry')} value={String(shown.expiresInHours)} disabled={locked || busy}
        options={expiryChoices.map(hours => ({value: String(hours), label: t('next.settings.people.expiryDays', {count: hours / 24})}))}
        onValueChange={value => set({expiresInHours: Number(value)})} />
      {signIn === 'portico' && <TextField label={t('next.settings.people.porticoUsername')} description={t('next.settings.people.porticoUsernameHelp')}
        disabled={locked || busy} value={shown.porticoUsername ?? ''} onChange={event => set({porticoUsername: event.currentTarget.value})} />}
      <TextField label={t('next.settings.people.notes')} description={t('next.settings.people.notesHelp')} disabled={locked || busy}
        value={shown.notes ?? ''} onChange={event => set({notes: event.currentTarget.value})} />
      <SettingsAdvanced open={advancedOpen} onOpenChange={setAdvancedOpen} summary={advancedSummary}>
        <label className={styles.choice}>
          <Switch checked={shown.accessTokens} disabled={locked || busy} onCheckedChange={accessTokens => set({accessTokens})} />
          <span>{t(`${spec}advanced.groups.0.rows.0.label`)}<small>{t(`${spec}advanced.groups.0.rows.0.help`)}</small></span>
        </label>
        <RichSelect label={t(`${spec}advanced.groups.0.rows.1.label`)} description={t(`${spec}advanced.groups.0.rows.1.help`)} value={shown.maxRating || 'none'} disabled={locked || busy}
          options={ratingChoices.map((value, index) => ({value: value || 'none', label: t(`${spec}advanced.groups.0.rows.1.control.options.${index}.label` as never),
            ...(index > 0 ? {description: t(`${spec}advanced.groups.0.rows.1.control.options.${index}.hint` as never)} : {})}))}
          onValueChange={value => set({maxRating: value === 'none' ? '' : value})} />
      </SettingsAdvanced>
    </div>
  </SettingsPanel>;
}
