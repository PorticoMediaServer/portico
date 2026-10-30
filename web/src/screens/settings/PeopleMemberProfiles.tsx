import {useEffect, useRef, useState, type CSSProperties} from 'react';
import {profileArtColors} from '@design';
import {defaultI18n} from '@i18n';
import {PROFILE_ART, type ProfileArt} from '@core/profile-management.ts';
import type {MemberProfile, MemberProfilesClient} from '@core/member-profiles.ts';
import {AVATAR_MAX_BYTES} from '@core/identity-client.ts';
import type {RatingSystem} from '@core/identity-restrictions.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Button, RichSelect, Switch, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Artwork} from '../../kit/artwork';
import {Dialog} from '../../kit/overlays/Dialog';
import {SettingsAction, SettingsStringList} from '../../kit/settings';
import type {PeopleMemberProfilesApi} from './people-member-profiles-api';
import styles from './account-profile/profile.module.css';
import local from './people-member-profiles.module.css';

const t = defaultI18n.t;
// Profile & account's own words for the same editor.
const K = {
  profiles: 'next.settingsSpec.account.profile.groups.0.rows.0.label', name: 'next.settingsSpec.account.profile.groups.1.rows.0.label',
  color: 'next.settingsSpec.account.profile.groups.1.rows.1.label', pin: 'next.settingsSpec.account.profile.groups.2.rows.0.label',
  rating: 'next.settingsSpec.account.profile.groups.3.rows.0.label', noLimit: 'next.settingsSpec.account.profile.groups.3.rows.0.control.options.0.label',
  unrated: 'next.settingsSpec.account.profile.groups.3.rows.1.label', labels: 'next.settingsSpec.account.profile.groups.3.rows.2.label',
  addLabel: 'next.settingsSpec.account.profile.groups.3.rows.2.control.addLabel',
} as const;
const colorNames = ['next.settingsSpec.account.profile.groups.1.rows.1.control.options.0.label', 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.1.label',
  'next.settingsSpec.account.profile.groups.1.rows.1.control.options.2.label', 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.3.label',
  'next.settingsSpec.account.profile.groups.1.rows.1.control.options.4.label', 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.5.label',
  'next.settingsSpec.account.profile.groups.1.rows.1.control.options.6.label', 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.7.label'] as const;
const flags = [['allowDownloads', 'next.settingsSpec.account.profile.groups.3.rows.3.label'], ['allowLiveTv', 'next.settingsSpec.account.profile.groups.3.rows.4.label'],
  ['allowDvr', 'next.settingsSpec.account.profile.groups.3.rows.5.label'], ['allowWatchTogether', 'next.settingsSpec.account.profile.groups.3.rows.6.label']] as const;

/** The Profiles tab of a person's panel (server-people spec): each real profile
 * with what sets it apart, and Manage opening the editor Profile & account
 * uses for one's own, scoped to this person by the server's own authority. */
/** `serverOwner`: only the server owner may switch a profile's Recordings on (Who can record);
 * an administrator may switch it off. */
export function PeopleMemberProfiles({api, accountId, canEdit, serverOwner = false}: {api: PeopleMemberProfilesApi; accountId: string; canEdit: boolean; serverOwner?: boolean}) {
  const [items, setItems] = useState<readonly MemberProfile[]>(), [systems, setSystems] = useState<readonly RatingSystem[]>([]), [error, setError] = useState<unknown>();
  const [managing, setManaging] = useState<string>(), [reload, setReload] = useState(0);
  const client = useRef<MemberProfilesClient | undefined>(undefined);
  useEffect(() => {
    const life = new AbortController(), profiles = api.forPerson(accountId);
    client.current = profiles; setItems(undefined); setError(undefined); setManaging(undefined);
    void Promise.all([profiles.list(life.signal), api.ratingSystems(life.signal)]).then(([list, ratings]) => {
      if (!life.signal.aborted) {setItems(list); setSystems(ratings);}
    }, failure => {if (!life.signal.aborted) setError(failure);});
    return () => {life.abort(); if (client.current === profiles) client.current = undefined;};
  }, [api, accountId, reload]);
  if (error && !items) return <div role="alert">{presentError(error, 'settings').body} <Button size="xs" variant="glass" onClick={() => setReload(value => value + 1)}>{t('action.tryAgain')}</Button></div>;
  if (!items) return <p>{t('status.loading')}</p>;
  const editing = items.find(item => item.id === managing);
  const replace = (next: MemberProfile) => setItems(current => current?.map(item => item.id === next.id ? next : item));
  return <>
    <ul className={styles.profiles} aria-label={t(K.profiles)}>
      {items.map(profile => {
        const rating = profile.restrictions.maximumAgeRating;
        const meta = [profile.primary ? t('profile.main') : '', profile.pinRequired ? t('profile.protected') : '', rating ? `${t(K.rating)} ${rating}` : ''].filter(Boolean).join(', ');
        return <li key={profile.id}>
          <MemberProfilePicture profile={profile} api={api} accountId={accountId} />
          <span className={styles.profileName}><b>{profile.name}</b>{meta && <small>{meta}</small>}</span>
          <span className={styles.profileActions}>{canEdit && <Button size="xs" variant="glass" onClick={() => setManaging(profile.id)}>
            {t('next.settingsSpec.server.people.panels.person.tabs.2.groups.0.rows.0.control.itemAction.label')}</Button>}</span>
        </li>;
      })}
    </ul>
    {canEdit && editing && client.current && <MemberProfileDialog key={editing.id} profile={editing} client={client.current} api={api} accountId={accountId} systems={systems} serverOwner={serverOwner}
      onSaved={replace} onClose={() => setManaging(undefined)} />}
  </>;
}

function MemberProfilePicture({profile, api, accountId, large = false}: {profile: MemberProfile; api: PeopleMemberProfilesApi; accountId: string; large?: boolean}) {
  const fallback = <span className={local.pictureFallback}><Icon name="i-user" size={large ? 40 : 20} /></span>;
  return <span className={`${styles.profileIcon} ${large ? local.picturePreview : local.pictureIcon}`} style={{'--profile-color': profileArtColors[profile.art]} as CSSProperties}>
    <Artwork source={api.picture(accountId, profile.avatar, large ? 160 : 64)} alt="" fallback={fallback} />
  </span>;
}

type Draft = Readonly<{name: string; art: ProfileArt; rating: string; allowUnrated: boolean; labels: readonly string[]; allowDownloads: boolean; allowLiveTv: boolean; allowDvr: boolean; allowWatchTogether: boolean}>;
const draftOf = (p: MemberProfile): Draft => ({name: p.name, art: p.art, rating: p.restrictions.maximumAgeRating, allowUnrated: p.restrictions.allowUnrated, labels: p.restrictions.blockedLabels,
  allowDownloads: p.restrictions.allowDownloads, allowLiveTv: p.restrictions.allowLiveTv, allowDvr: p.restrictions.allowDvr, allowWatchTogether: p.restrictions.allowWatchTogether});

function MemberProfileDialog({profile, client, api, accountId, systems, serverOwner, onSaved, onClose}: {profile: MemberProfile; client: MemberProfilesClient; api: PeopleMemberProfilesApi; accountId: string; systems: readonly RatingSystem[]; serverOwner: boolean;
  onSaved: (profile: MemberProfile) => void; onClose: () => void}) {
  const [draft, setDraft] = useState<Draft>(() => draftOf(profile)), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [removing, setRemoving] = useState(false);
  const [pictureError, setPictureError] = useState<'size' | 'type'>();
  const closed = useRef(false), pending = useRef(false), picker = useRef<HTMLInputElement>(null), pictureTarget = useRef<MemberProfile | undefined>(undefined);
  useEffect(() => {closed.current = false; return () => {closed.current = true; pictureTarget.current = undefined;};}, []);
  const set = (patch: Partial<Draft>) => setDraft(current => ({...current, ...patch}));
  // The rating scale is the profile's own system, or the first published one for a profile with none yet.
  const system = systems.find(item => item.id === profile.restrictions.ratingSystem) ?? (profile.restrictions.ratingSystem === '' ? systems[0] : undefined);
  const named = draft.name.trim() !== profile.name || draft.art !== profile.art;
  const r = profile.restrictions;
  const limited = draft.rating !== r.maximumAgeRating || draft.allowUnrated !== r.allowUnrated || draft.labels.join('\n') !== r.blockedLabels.join('\n')
    || flags.some(([key]) => draft[key] !== r[key]);
  const run = async (work: () => Promise<MemberProfile | undefined>, close = true) => {
    if (closed.current || pending.current) return;
    pending.current = true; setBusy(true); setError(undefined); setPictureError(undefined);
    try {const next = await work(); if (next && !closed.current) onSaved(next); if (close && !closed.current) onClose();}
    catch (failure) {if (!closed.current) setError(failure);}
    finally {pending.current = false; if (!closed.current) setBusy(false);}
  };
  const picture = (target: MemberProfile, file?: File) => {
    if (closed.current || pending.current) return;
    if (file && (!file.size || file.size > AVATAR_MAX_BYTES)) {setPictureError('size'); return;}
    if (file && file.type !== 'image/jpeg' && file.type !== 'image/png') {setPictureError('type'); return;}
    void run(() => file ? client.uploadPicture(target, file, file.type) : client.removePicture(target), false);
  };
  const save = () => run(async () => {
    let current = profile;
    if (named) current = await client.rename(current, {...(draft.name.trim() !== profile.name ? {name: draft.name.trim()} : {}), ...(draft.art !== profile.art ? {art: draft.art} : {})});
    if (limited) current = await client.restrict(current, {ratingSystem: draft.rating ? system?.id ?? r.ratingSystem : r.ratingSystem, maximumAgeRating: draft.rating, allowUnrated: draft.allowUnrated,
      blockedLabels: draft.labels, allowDownloads: draft.allowDownloads, allowLiveTv: draft.allowLiveTv, allowDvr: draft.allowDvr, allowWatchTogether: draft.allowWatchTogether});
    return current;
  });
  return <>
    <Dialog open width={480} title={profile.name} dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy} onOpenChange={open => {if (!open && !busy) onClose();}}
      footer={<><Button size="small" disabled={busy} onClick={onClose}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={busy} disabled={busy || !draft.name.trim() || draft.name.trim().length > 120 || !named && !limited} onClick={() => {void save();}}>{t('action.save')}</Button></>}>
      <div className={styles.fields}>
        {error ? <p role="alert">{presentError(error, 'settings').body}</p> : null}
        <div className={local.picture}>
          <MemberProfilePicture profile={{...profile, art: draft.art}} api={api} accountId={accountId} large />
          <div className={local.pictureButtons}>
            <input ref={picker} type="file" hidden accept="image/jpeg,image/png" onChange={event => {
              const file = event.currentTarget.files?.[0], target = pictureTarget.current;
              event.currentTarget.value = ''; pictureTarget.current = undefined;
              if (file && target) picture(target, file);
            }} />
            <Button size="small" variant="glass" disabled={busy} onClick={() => {if (!pending.current && !closed.current) {pictureTarget.current = profile; picker.current?.click();}}}>{t('next.settingsSpec.account.profile.groups.1.rows.2.control.label')}</Button>
            {profile.avatar && <Button size="small" variant="glass" disabled={busy} onClick={() => picture(profile)}>{t('next.settingsSpec.account.profile.groups.1.rows.2.control.second.label')}</Button>}
          </div>
        </div>
        {pictureError && <p role="alert" className={styles.pictureError}>{t(pictureError === 'size' ? 'web.profile.pictureTooLarge' : 'profile.pictureRejected')}</p>}
        <TextField label={t(K.name)} value={draft.name} maxLength={120} disabled={busy} autoComplete="off" onChange={event => set({name: event.currentTarget.value})} />
        <fieldset className={styles.colors} disabled={busy}><legend>{t(K.color)}</legend><div role="radiogroup" aria-label={t(K.color)}>
          {PROFILE_ART.map((color, index) => <button key={color} type="button" role="radio" aria-checked={draft.art === color}
            aria-label={t(colorNames[index]!)} style={{'--profile-color': profileArtColors[color]} as CSSProperties} onClick={() => set({art: color})} />)}
        </div></fieldset>
        <RichSelect label={t(K.rating)} value={draft.rating || 'none'} disabled={busy || !system}
          options={[{value: 'none', label: t(K.noLimit)}, ...(system?.values.map(value => ({value: value.code, label: value.label})) ?? [])]}
          onValueChange={value => set({rating: value === 'none' ? '' : value})} />
        <label className={local.toggle}><span>{t(K.unrated)}</span><Switch checked={draft.allowUnrated} disabled={busy} onCheckedChange={allowUnrated => set({allowUnrated})} /></label>
        <SettingsStringList label={t(K.labels)} values={draft.labels} addLabel={t(K.addLabel)} disabled={busy} onChange={labels => set({labels})} />
        {flags.map(([key, label]) => <label key={key} className={local.toggle}><span>{t(label)}</span>
          <Switch checked={draft[key]} disabled={busy || key === 'allowDvr' && !serverOwner && !profile.restrictions.allowDvr} onCheckedChange={value => set({[key]: value} as Partial<Draft>)} /></label>)}
        {profile.pinRequired && <div className={local.pin}><b>{t(K.pin)}</b><small>{t('profile.protected')}</small><Button size="xs" variant="glass" disabled={busy} onClick={() => setRemoving(true)}>{t('profile.removePin')}</Button></div>}
      </div>
    </Dialog>
    <Dialog open={removing} width={420} title={t('profile.removePinConfirm.title', {name: profile.name})} description={t('profile.removePinConfirm.body', {name: profile.name})}
      onOpenChange={open => {if (!open && !busy) setRemoving(false);}} showCloseButton={!busy}
      footer={<><Button size="small" disabled={busy} onClick={() => setRemoving(false)}>{t('action.cancel')}</Button>
        <SettingsAction appearance="danger" size="small" loading={busy} onClick={() => {setRemoving(false); void run(() => client.resetPIN(profile));}}>{t('profile.removePin')}</SettingsAction></>}><></></Dialog>
  </>;
}
