import {AppPasswordsPanel} from './AppPasswords';
import type {ApiAccessTransport} from '@core/api-access.ts';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {ApiCredential, ApiScope, IssuedApiCredential} from '@core/api-access.ts';
import {EXPIRY_DAYS, chosenScopes, credentialState, credentialWorks, expiryInstant, expiryLabel, scopeTicked, scopesText, type ExpiryDays} from '@core/server-admin/api-words.ts';
import {useRouterState} from '@tanstack/react-router';
import {Tabs} from '../../../../kit/controls/Choices';
import {Button, Select, TextField} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays/Dialog';
import {Icon} from '../../../../kit/Icon';
import {AssistantsPanel} from '../../integrations/Assistants';
import {SettingsApiKeysSource} from './api-keys-source';
import {WebhooksPanel} from './Webhooks';
import type {SettingsWebhooksSource} from './webhooks-source';
import styles from './integrations.module.css';

const t = defaultI18n.t;
type IssuedCard = Readonly<{source: SettingsApiKeysSource; value: IssuedApiCredential; token: object}>;
type FormAttempt = Readonly<{source: SettingsApiKeysSource}>;
type Props = Readonly<{api:ApiAccessTransport; source: SettingsApiKeysSource; webhooks: SettingsWebhooksSource; baseUrl: string; isCurrent: () => boolean; reportError: (error: unknown) => void; otherRows: ReactNode; advancedRows: ReactNode}>;

/** The owner's real integration sources; advanced settings retain their
 * existing controller, while Assistants invokes this same key creator. */
type IntegrationsTab = 'keys' | 'webhooks' | 'assistants' | 'compatibility';
const tabForRow = (row: string | null): IntegrationsTab => row === 'integrations.webhooks' ? 'webhooks' : row?.startsWith('integrations.assistant') ? 'assistants' : 'keys';

export function IntegrationsPage({api, source, webhooks, baseUrl, isCurrent, reportError, otherRows, advancedRows}: Props) {
  const requestedRow = useRouterState({select: state => new URLSearchParams(state.location.searchStr).get('row')});
  const [tab, setTab] = useState<IntegrationsTab>(() => tabForRow(requestedRow));
  useEffect(() => {setTab(tabForRow(requestedRow));}, [requestedRow]);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [creating, setCreating] = useState(false), [name, setName] = useState(''), [ticked, setTicked] = useState<ReadonlySet<ApiScope>>(new Set()), [days, setDays] = useState<ExpiryDays>(0);
  const [issued, setIssued] = useState<IssuedCard>(), [copied, setCopied] = useState(false), [ended, setEnded] = useState(false), [revoking, setRevoking] = useState<ApiCredential>();
  const [cursors, setCursors] = useState<readonly string[]>(['']);
  const revealAttempt = useRef<object | undefined>(undefined);
  const card = useRef<IssuedCard | undefined>(undefined), formAttempt = useRef<FormAttempt | undefined>(undefined);
  const generation = useRef<object>({}), paging = useRef(false), secret = useRef<HTMLTextAreaElement>(null);
  const scope = useRef(source);
  if (scope.current !== source) {scope.current = source;card.current = undefined;formAttempt.current = undefined;generation.current = {};}
  const heldForm = formAttempt.current;
  useLayoutEffect(() => {
    generation.current = {};card.current = undefined;formAttempt.current = undefined;setIssued(undefined);setCreating(false);setRevoking(undefined);setCursors(['']);void source.load();
    return () => {generation.current = {};card.current = undefined;formAttempt.current = undefined;};
  }, [source]);
  const live = isCurrent() && state.phase !== 'idle', heldCard = live && issued?.source === source && card.current === issued ? issued : undefined, heldIssue = heldCard?.value;
  const closeForm = () => {formAttempt.current = undefined;setCreating(false);};
  const closeCard = () => {revealAttempt.current=undefined;card.current = undefined;setIssued(undefined);setCopied(false);};
  const offered = state.options?.scopes ?? [], chosen = chosenScopes(ticked).filter(scope => offered.includes(scope));
  const busy = state.busy || state.phase === 'loading', frozen = state.busy || state.pendingCreate;
  const working = state.page?.items.filter(credentialWorks) ?? [], stopped = state.page?.items.filter(key => !credentialWorks(key)) ?? [];
  const start = () => {
    if (!isCurrent() || state.busy) return;
    if (!state.pendingCreate) {setName('');setTicked(new Set(offered.includes('read') ? ['read'] : []));setDays(0);}
    formAttempt.current = {source};setCreating(true);
  };
  const submit = async () => {
    const attempt = heldForm;
    if (!isCurrent() || state.busy || !attempt || formAttempt.current !== attempt || attempt.source !== source) return;
    const held = generation.current;
    const out = await (state.pendingCreate ? source.retryCreate() : source.create({name, scopes: chosen, ...(days ? {expiresAt: expiryInstant(days, Date.now())} : {})}));
    if (out && isCurrent() && held === generation.current && formAttempt.current === attempt) {closeForm();const next = {source, value: out, token: {}};card.current = next;setCopied(false);setIssued(next);}
  };
  const copy = async () => {
    const captured = heldCard;
    const ownsCard = () => isCurrent() && !!captured && captured.source === source && card.current === captured;
    if (!ownsCard() || !captured?.value.secret) return;
    try {await navigator.clipboard.writeText(captured.value.secret);if (ownsCard()) setCopied(true);}
    catch {if (ownsCard()) {setCopied(false);secret.current?.focus();secret.current?.select();}}
  };
  const reveal = async (key:ApiCredential) => {
    closeCard();const attempt={};revealAttempt.current=attempt;const held=generation.current;
    const value=await source.reveal(key);
    if(value&&isCurrent()&&source.getSnapshot().phase!=='idle'&&held===generation.current&&revealAttempt.current===attempt){const next={source,value:{credential:key,secret:value},token:{}};card.current=next;setIssued(next);}
  };
  const page = async (cursor: string, next: readonly string[]) => {
    if (!isCurrent() || paging.current || state.busy) return;
    paging.current = true;const held = generation.current;
    try {await source.load(cursor);if (isCurrent() && held === generation.current && source.getSnapshot().phase === 'ready') setCursors(next);}
    finally {paging.current = false;}
  };
  const row = (key: ApiCredential) => {
    const badge = credentialState(t, defaultI18n.has, key);
    return <li key={key.id} className={styles.keyRow}>
      <span className={styles.keyIcon}><Icon name="i-key" size={20} /></span>
      <div className={styles.keyText}><b>{key.name}</b><div className={styles.facts}>
        <span>{scopesText(t, key.scopes)}</span>{key.hint && <span>{t('api.credential.endsIn', {hint: key.hint})}</span>}
        <span>{key.lastUsedAt ? t('api.credential.lastUsed', {when: defaultI18n.relativeTime(key.lastUsedAt)}) : t('api.credential.neverUsed')}</span>
        <span>{key.expiresAt ? t('api.credential.expires', {when: defaultI18n.date(key.expiresAt)}) : t('api.credential.noExpiry')}</span>
      </div></div>
      <div className={styles.actions}><span className={styles.badge} data-tone={badge?.tone ?? 'healthy'}>{badge?.text ?? t('api.credential.state.active')}</span>
        {key.state==='active'&&<Button size="xs" variant="text" disabled={busy} onClick={()=>{void reveal(key);}}>{t('integrations.credential.reveal')}</Button>}
        {credentialWorks(key) && <Button size="xs" variant="text" disabled={busy} onClick={() => {if (isCurrent()) setRevoking(key);}}>{t('web.access.revoke')}</Button>}</div>
    </li>;
  };
  return <div className={styles.page}>
    <Tabs aria-label={t('next.settingsSpec.server.integrations.title')} value={tab} onValueChange={value => {if (value === 'keys' || value === 'webhooks' || value === 'assistants' || value === 'compatibility') setTab(value);}} items={[
      {value: 'keys', label: t('web.access.apiKeys'), panel: <section id="integrations.apiKeys" aria-labelledby="api-keys-title">
      <div className={styles.heading}><h3 id="api-keys-title">{t('web.access.apiKeys')}</h3><Button size="xs" variant="glass" disabled={busy || !state.options} onClick={start}>{t('web.access.createKey')}</Button></div>
      <p className={styles.lede}>{t('server.access.keyLede')}</p>
      {state.error && <div className={styles.failure} role="alert"><span>{t(state.error === 'read' ? 'error.unavailable' : 'error.unconfirmedChange')}</span><Button size="xs" variant="glass" disabled={busy} onClick={() => {if (isCurrent()) void source.load();}}>{t('action.refresh')}</Button></div>}
      {state.phase === 'loading' && !state.page && <p role="status" className={styles.note}>{t('next.settings.loading')}</p>}
      {state.page && <><ul className={styles.keys}>{working.map(row)}{ended && stopped.map(row)}</ul>{!working.length && <p className={styles.note}>{t('web.access.noApiKeys')}</p>}
        {stopped.length > 0 && <Button size="xs" variant="text" aria-expanded={ended} onClick={() => setEnded(value => !value)}>{t(ended ? 'api.credential.hideEnded' : 'api.credential.showEnded', {count: stopped.length})}</Button>}
        {(cursors.length > 1 || state.page.nextCursor) && <div className={styles.pager}><Button size="xs" variant="glass" disabled={busy || cursors.length < 2} onClick={() => {const next = cursors.slice(0, -1);void page(next[next.length - 1] ?? '', next);}}>{t('web.pager.previous')}</Button>
          <Button size="xs" variant="glass" disabled={busy || !state.page.nextCursor} onClick={() => {const next = state.page?.nextCursor;if (next) void page(next, [...cursors, next]);}}>{t('web.pager.next')}</Button></div>}
      </>}
      <p className={styles.footnote}>{t('api.tokens.lifetime')}</p>
    </section>},
      {value: 'compatibility', label:t('integrations.appPasswords.title'),panel:<AppPasswordsPanel api={api} owner={source} isCurrent={isCurrent} reportError={reportError}/>},
      {value: 'webhooks', label: t('api.webhooks.title'), panel: <WebhooksPanel source={webhooks} isCurrent={isCurrent} />},
      {value: 'assistants', label: t('api.mcp.tab'), panel: <div id="integrations.assistantAddress"><div id="integrations.assistantKey"><AssistantsPanel baseUrl={baseUrl} isCurrent={isCurrent} openKeys={() => {if (isCurrent()) setTab('keys');}} reportError={reportError} /></div></div>},
    ]} />
    {otherRows}
    {advancedRows}
    <Dialog open={live && creating} onOpenChange={open => {if (!open && !state.busy) closeForm();}} title={t('web.access.createKeyTitle')} width={500}
      dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy}
      footer={<><Button variant="glass" size="compact" disabled={state.busy} onClick={closeForm}>{t('action.cancel')}</Button><Button variant="primary" size="compact" disabled={state.busy || (!state.pendingCreate && (!name.trim() || !chosen.length || !state.options))} onClick={() => {void submit();}}>{t(state.pendingCreate ? 'action.tryAgain' : 'web.saved.create')}</Button></>}>
      <div className={styles.form}>
        {state.error === 'create' && <p className={styles.failure} role="alert">{t('error.unconfirmedChange')}</p>}
        <TextField label={t('profile.name')} description={t('web.access.keyNameHelp')} maxLength={80} disabled={frozen} value={name} onChange={event => setName(event.target.value)} />
        <fieldset className={styles.scopes} disabled={frozen}><legend>{t('web.access.scopeLabel')}</legend>{offered.map(scope => <label key={scope} className={styles.scope}>
          <input type="checkbox" checked={scopeTicked(ticked, scope)} disabled={scope !== 'admin' && ticked.has('admin')} onChange={event => setTicked(previous => {const next = new Set(previous);if (event.target.checked) next.add(scope);else next.delete(scope);return next;})} />
          <span><b>{t(`api.scope.${scope}` as MessageId)}</b><small>{t(`api.scope.${scope}.help` as MessageId)}</small></span>
        </label>)}</fieldset>
        <p className={styles.note}>{t('api.scopes.note')}</p>
        <Select label={t('api.credential.expiryLabel')} value={String(days)} disabled={frozen} options={EXPIRY_DAYS.map(value => ({value: String(value), label: expiryLabel(t, value)}))} onValueChange={value => setDays(Number(value) as ExpiryDays)} />
      </div>
    </Dialog>
    <Dialog open={!!heldIssue} onOpenChange={open => {if (!open && !heldIssue?.secret) closeCard();}} title={t('web.access.copyKeyTitle')} width={500} initialFocusRef={secret}
      dismissOnOutsidePress={!heldIssue?.secret} dismissOnBack={!heldIssue?.secret} showCloseButton={false}
      footer={<Button variant="primary" size="compact" onClick={closeCard}>{t('action.done')}</Button>}>
      {heldIssue?.secret ? <div className={styles.form}><p className={styles.warning}>{t('integrations.credential.recoverable')}</p><label className={styles.secretLabel}>{t('web.access.apiKey')}<textarea ref={secret} className={styles.secret} readOnly rows={3} value={heldIssue.secret} onFocus={event => event.currentTarget.select()} /></label>
        <div className={styles.actions}><Button size="xs" variant="glass" icon="i-copy" onClick={() => {void copy();}}>{t('web.twoStep.copy')}</Button><span className={styles.copied} role="status">{copied && t('api.credential.copied')}</span></div></div>
        : <p className={styles.warning}>{t('integrations.credential.retryReveal')}</p>}
    </Dialog>
    <Dialog open={live && !!revoking} onOpenChange={open => {if (!open && !state.busy) setRevoking(undefined);}} title={t('web.access.revokeTitle', {name: revoking?.name ?? ''})} description={t('web.access.revokeBody')} width={460}
      dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy}
      footer={<><Button size="compact" variant="glass" disabled={state.busy} onClick={() => setRevoking(undefined)}>{t('action.cancel')}</Button><Button size="compact" variant="glass" className={styles.danger} disabled={state.busy} onClick={() => {if (!revoking || !isCurrent()) return;const held = generation.current;void source.revoke(revoking).then(() => {if (isCurrent() && held === generation.current && !source.getSnapshot().error) setRevoking(undefined);});}}>{t('web.access.revoke')}</Button></>}>
      {state.error === 'revoke' && <p className={styles.failure} role="alert">{t('error.unconfirmedChange')}</p>}
    </Dialog>
  </div>;
}
