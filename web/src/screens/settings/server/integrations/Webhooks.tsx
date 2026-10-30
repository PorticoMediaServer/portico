import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {IssuedWebhook, Webhook} from '@core/api-access.ts';
import {webhookEdits} from '@core/api-access.ts';
import {deliveryProblem, deliveryProgress, webhookAddressOk, webhookEventGroups, webhookEventLabel, webhookTrouble} from '@core/server-admin/api-words.ts';
import {Button, Switch, TextField} from '../../../../kit/controls';
import {Icon, type IconName} from '../../../../kit/Icon';
import {Dialog} from '../../../../kit/overlays/Dialog';
import {usePhone} from '../../../../kit/overlays/shared';
import {Menu} from '../../../../kit/overlays/Popover';
import {SettingsWebhooksSource} from './webhooks-source';
import styles from './integrations.module.css';
const t = defaultI18n.t;
/** The server's own rule for a signing secret: 16 to 128 printable ASCII
 * characters, with no spaces. Empty means it makes one. */
const webhookSecretOk = (secret: string) => secret.length >= 16 && secret.length <= 128 && [...secret].every(value => value >= '!' && value <= '~');
type Editor = Readonly<{source: SettingsWebhooksSource; hook?: Webhook}>;
type Confirmation = Readonly<{source: SettingsWebhooksSource; hook: Webhook; kind: 'remove'|'rotate'}>;
type SecretCard = Readonly<{source: SettingsWebhooksSource; issued: IssuedWebhook}>;
type RowSheet = Readonly<{source: SettingsWebhooksSource; hook: Webhook}>;
/** The row's own actions, as the phone's sheet lists them (frame 39a). */
const rowActions: readonly {kind: 'edit'|'test'|'log'|'rotate'|'remove'; label: MessageId; icon: IconName; danger?: boolean}[] = [
  {kind: 'edit', label: 'web.selection.edit', icon: 'i-edit'},
  {kind: 'test', label: 'api.webhooks.sendTest', icon: 'i-send'},
  {kind: 'log', label: 'api.webhooks.log', icon: 'i-logs'},
  {kind: 'rotate', label: 'api.webhooks.rotate', icon: 'i-refresh'},
  {kind: 'remove', label: 'web.missingTitles.removeConfirm', icon: 'i-trash', danger: true},
];

function HookMenu({hook, disabled, action}: Readonly<{hook: Webhook; disabled: boolean; action: (kind: 'edit'|'test'|'log'|'rotate'|'remove') => void}>) {
  const [open, setOpen] = useState(false);
  return <Menu open={open} onOpenChange={setOpen} title={t('api.webhooks.actions', {name: hook.name})} width={232}
    trigger={<Button size="xs" variant="text" icon="i-more" disabled={disabled} aria-label={t('api.webhooks.actions', {name: hook.name})} />}
    items={[
      {id:'edit',label:t('web.selection.edit'),icon:'i-edit',onSelect:()=>action('edit')},
      {id:'test',label:t('api.webhooks.sendTest'),icon:'i-send',onSelect:()=>action('test')},
      {id:'log',label:t('api.webhooks.log'),icon:'i-logs',onSelect:()=>action('log')},
      {id:'separator',separator:true},
      {id:'rotate',label:t('api.webhooks.rotate'),icon:'i-refresh',onSelect:()=>action('rotate')},
      {id:'remove',label:t('web.missingTitles.removeConfirm'),icon:'i-trash',destructive:true,onSelect:()=>action('remove')},
    ]} />;
}

export function WebhooksPanel({source, isCurrent}: Readonly<{source: SettingsWebhooksSource; isCurrent: () => boolean}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const phone = usePhone();
  const [editor, setEditor] = useState<Editor>(), [confirmation, setConfirmation] = useState<Confirmation>(), [card, setCard] = useState<SecretCard>(), [rowSheet, setRowSheet] = useState<RowSheet>();
  const [name, setName] = useState(''), [url, setUrl] = useState(''), [events, setEvents] = useState<ReadonlySet<string>>(new Set()), [signing, setSigning] = useState(''), [rotateSecret, setRotateSecret] = useState(''), [copied, setCopied] = useState(false), [testQueued, setTestQueued] = useState(false);
  const [logCursors, setLogCursors] = useState<readonly string[]>(['']);
  const editorRef = useRef<Editor | undefined>(undefined), confirmationRef = useRef<Confirmation | undefined>(undefined), cardRef = useRef<SecretCard | undefined>(undefined), rowRef = useRef<RowSheet | undefined>(undefined), testAttempt = useRef<object | undefined>(undefined), scope = useRef(source), secret = useRef<HTMLTextAreaElement>(null);
  if (scope.current !== source) {scope.current = source;editorRef.current = undefined;confirmationRef.current = undefined;cardRef.current = undefined;rowRef.current = undefined;testAttempt.current = undefined;}
  useLayoutEffect(() => {setEditor(undefined);setConfirmation(undefined);setCard(undefined);setRowSheet(undefined);setSigning('');setRotateSecret('');void source.load();return () => {editorRef.current = undefined;confirmationRef.current = undefined;cardRef.current = undefined;rowRef.current = undefined;testAttempt.current = undefined;setSigning('');setRotateSecret('');};}, [source]);
  const live = isCurrent(), busy = state.busy || state.phase === 'loading', writable = live && state.phase === 'ready' && !state.uncertain && !busy;
  const heldEditor = live && editor?.source === source && editorRef.current === editor ? editor : undefined;
  const heldConfirmation = live && confirmation?.source === source && confirmationRef.current === confirmation ? confirmation : undefined;
  const heldCard = live && card?.source === source && cardRef.current === card ? card : undefined;
  const heldRow = live && rowSheet?.source === source && rowRef.current === rowSheet ? rowSheet : undefined;
  // A close or field edit belongs to the exact dialog its render drew: a
  // retained handler cannot clear or edit a successor's secret.
  const closeEditor = (held: Editor | undefined) => {
    if (!held || editorRef.current !== held) return;
    editorRef.current = undefined;setEditor(undefined);setSigning('');
  };
  const closeConfirmation = (held: Confirmation | undefined) => {
    if (!held || confirmationRef.current !== held) return;
    confirmationRef.current = undefined;setConfirmation(undefined);setRotateSecret('');
  };
  const closeCard = () => {cardRef.current = undefined;setCard(undefined);setCopied(false);};
  const showIssued = (issued: IssuedWebhook) => {if (issued.secret) {const next = {source, issued};cardRef.current = next;setCopied(false);setCard(next);}};
  const edit = (hook?: Webhook) => {if (!writable) return;const next = {source, hook};editorRef.current = next;setName(hook?.name ?? '');setUrl(hook?.url ?? '');setEvents(new Set(hook?.events ?? []));setSigning('');setEditor(next);};
  const save = async () => {
    const held = heldEditor;if (!held || editorRef.current !== held || !writable || !name.trim() || !webhookAddressOk(url) || !events.size || (signing !== '' && !webhookSecretOk(signing))) return;
    const draft = {name: name.trim(), url: url.trim(), events: [...events]};
    const edits = held.hook ? webhookEdits(held.hook, draft) : undefined;
    // A webhook retired by an ownership transfer is reviewed only when the
    // destination and the whole subscription are saved explicitly; the drawn
    // dialog's Save is that review, whether or not they changed.
    const reviewed = held.hook?.requiresOwnerReview ? {...edits, url: draft.url, events: draft.events} : edits;
    const change = {...(reviewed ?? {}), ...(signing ? {secret: signing} : {})};
    const issued = held.hook ? await source.update(held.hook, change) : await source.create({...draft, ...(signing ? {secret: signing} : {})});
    if (issued && isCurrent() && editorRef.current === held) {closeEditor(held);showIssued(issued);}
  };
  const confirm = async () => {
    const held = heldConfirmation;if (!held || confirmationRef.current !== held || !writable || (held.kind === 'rotate' && rotateSecret !== '' && !webhookSecretOk(rotateSecret))) return;
    const out = held.kind === 'remove' ? await source.remove(held.hook) : await source.update(held.hook, rotateSecret ? {secret: rotateSecret} : {rotateSecret:true});
    if (out && isCurrent() && confirmationRef.current === held) {closeConfirmation(held);if (typeof out === 'object') showIssued(out);}
  };
  const copy = async () => {
    const held = heldCard;const owns = () => isCurrent() && !!held && cardRef.current === held;
    if (!owns() || !held?.issued.secret) return;
    try {await navigator.clipboard.writeText(held.issued.secret);if (owns()) setCopied(true);}
    catch {if (owns()) {setCopied(false);secret.current?.focus();secret.current?.select();}}
  };
  const action = (hook: Webhook, kind: 'edit'|'test'|'log'|'rotate'|'remove') => {
    if (!isCurrent() || !source.getSnapshot().list?.items.includes(hook)) return;
    if (kind !== 'test') testAttempt.current = undefined;
    if (kind === 'edit') edit(hook);
    else if (kind === 'log') {setLogCursors(['']);void source.select(hook);}
    else if (kind === 'test') {if (writable) {const attempt = {};testAttempt.current = attempt;setTestQueued(false);void source.test(hook).then(out => {if (out && isCurrent() && testAttempt.current === attempt) {setTestQueued(true);const fresh = source.getSnapshot().list?.items.find(item => item.id === hook.id);if (fresh) {setLogCursors(['']);void source.select(fresh);}}});}}
    else if (writable) {const next = {source, hook, kind};confirmationRef.current = next;setRotateSecret('');setConfirmation(next);}
  };
  const closeRow = (held: RowSheet | undefined) => {if (!held || rowRef.current !== held) return;rowRef.current = undefined;setRowSheet(undefined);};
  const openRow = (hook: Webhook) => {if (!isCurrent() || !source.getSnapshot().list?.items.includes(hook)) return;const next = {source, hook};rowRef.current = next;setRowSheet(next);};
  const rowAction = (held: RowSheet, kind: 'edit'|'test'|'log'|'rotate'|'remove') => {if (!held || rowRef.current !== held) return;closeRow(held);action(held.hook, kind);};
  const log = state.selected;
  const logPage = async (cursor: string, next: readonly string[]) => {if (!log || !isCurrent() || busy) return;await source.select(log, cursor);if (isCurrent() && source.getSnapshot().selected === log && source.getSnapshot().deliveries) setLogCursors(next);};
  return <section id="integrations.webhooks">
    <div className={styles.heading}><h3>{t('api.webhooks.title')}</h3><Button size="xs" variant="glass" icon="i-plus" disabled={!writable || !state.list || state.list.items.length >= state.list.maximum} onClick={() => edit()}>{t('api.webhooks.add')}</Button></div>
    <p className={styles.lede}>{t('api.webhooks.lede')}</p>
    {(state.error || state.uncertain) && <p role="alert" className={styles.failure}>{t(state.uncertain ? 'error.unconfirmedChange' : 'error.unavailable')}<Button size="xs" disabled={busy} onClick={() => {if (isCurrent()) {closeEditor(heldEditor);closeConfirmation(heldConfirmation);void source.reconcile();}}}>{t('action.refresh')}</Button></p>}
    {!state.list && busy && <p role="status">{t('next.settings.loading')}</p>}
    {testQueued && <p role="status" className={styles.note}>{t('api.webhooks.testQueued')}</p>}
    {state.list && <ul className={styles.keys}>{state.list.items.map(hook => {const trouble = webhookTrouble(t, hook);return <li className={styles.keyRow} key={hook.id} data-disabled={!hook.enabled || undefined}>
      <span className={styles.keyIcon}><Icon name="i-webhook" size={20}/></span>{(() => {
        const text = <><b>{hook.name}</b><span className={styles.facts}><span className={styles.mono}>{hook.url}</span><span>{t('api.webhooks.eventCount', {count:hook.events.length})}</span>{trouble && <span className={styles.hookTrouble} data-tone={trouble.tone}>{trouble.text}</span>}{hook.requiresOwnerReview ? <span className={styles.hookTrouble} data-tone="warning">{t('api.webhooks.ownerReviewRequired')}</span> : null}</span></>;
        return phone ? <button type="button" className={`${styles.keyText} ${styles.keyOpen}`} disabled={!live || busy} onClick={() => openRow(hook)}>{text}</button> : <div className={styles.keyText}>{text}</div>;
      })()}
      <div className={styles.actions}><Switch checked={hook.enabled} disabled={!writable} aria-label={t('api.webhooks.turnOn', {name:hook.name})} onCheckedChange={enabled => {if (!isCurrent() || !writable) return;if (!source.getSnapshot().list?.items.includes(hook)) return;if (enabled && hook.requiresOwnerReview) {const step = source.reviewStep(hook);if (step === 'urlEvents') {edit(hook);return;}if (step === 'secret') {const next = {source, hook, kind: 'rotate' as const};confirmationRef.current = next;setRotateSecret('');setConfirmation(next);return;}}void source.update(hook, {enabled});}}/>{!phone && <HookMenu hook={hook} disabled={busy || !live} action={kind => action(hook, kind)}/>}</div>
    </li>;})}</ul>}
    {state.list && state.list.items.length >= state.list.maximum && <p className={styles.note}>{t('api.webhooks.full', {count:state.list.maximum})}</p>}
    {state.list && !state.list.items.length && <p className={styles.note}>{t('api.webhooks.none')}</p>}
    <Dialog open={!!heldEditor} onOpenChange={open => {if (!open && !state.busy) closeEditor(heldEditor);}} title={t(heldEditor?.hook ? 'api.webhooks.editTitle' : 'api.webhooks.add')} width={600} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy}
      footer={<><Button variant="glass" size="compact" disabled={state.busy} onClick={() => closeEditor(heldEditor)}>{t('action.cancel')}</Button><Button variant="primary" size="compact" disabled={!writable || !name.trim() || !webhookAddressOk(url) || !events.size || (signing !== '' && !webhookSecretOk(signing))} onClick={() => {void save();}}>{t(heldEditor?.hook ? 'action.save' : 'api.webhooks.add')}</Button></>}>
      <div className={styles.form}><TextField label={t('profile.name')} value={name} maxLength={160} disabled={state.busy || state.uncertain} onChange={event => setName(event.target.value)}/><TextField label={t('api.webhooks.url')} value={url} maxLength={2048} disabled={state.busy || state.uncertain} description={t('api.webhooks.urlHelp')} error={url && !webhookAddressOk(url) ? t('api.webhooks.urlProblem') : undefined} onChange={event => setUrl(event.target.value)}/>
      <fieldset className={styles.scopes} disabled={state.busy || state.uncertain}><legend>{t('api.webhooks.events')}</legend>{webhookEventGroups(state.list?.events ?? []).map(group => <div key={group.id}><div className={styles.eventHeading}><b>{t(group.label)}</b><span className={styles.eventActions}><Button size="xs" variant="text" aria-label={t('api.webhooks.groupAllOf', {group: t(group.label)})} onClick={() => setEvents(previous => new Set([...previous, ...group.events]))}>{t('api.webhooks.groupAll')}</Button><Button size="xs" variant="text" aria-label={t('api.webhooks.groupNoneOf', {group: t(group.label)})} onClick={() => setEvents(previous => {const next = new Set(previous);for (const event of group.events) next.delete(event);return next;})}>{t('api.webhooks.groupNone')}</Button></span></div><div className={styles.eventGroup}>{group.events.map(event => <label className={styles.scope} key={event}><input type="checkbox" checked={events.has(event)} onChange={e => setEvents(previous => {const next = new Set(previous);if (e.target.checked) next.add(event);else next.delete(event);return next;})}/><span>{webhookEventLabel(t, defaultI18n.has, event)}</span></label>)}</div></div>)}</fieldset>
      {heldEditor?.hook?.requiresOwnerReview ? <p className={styles.note}>{t('api.webhooks.ownerReviewRequired')}</p> : null}
      {heldEditor?.hook ? <p className={styles.note}>{t('api.webhooks.secretKept', {hint:heldEditor.hook.secretHint})}</p> : <TextField label={t('api.webhooks.secret')} description={t('api.webhooks.secretHelp')} type="password" autoComplete="new-password" value={signing} maxLength={256} disabled={state.busy || state.uncertain} error={signing !== '' && !webhookSecretOk(signing) ? t('api.webhooks.secretProblem') : undefined} onChange={event => {if (editorRef.current === heldEditor) setSigning(event.target.value);}}/>}</div>
    </Dialog>
    <Dialog open={!!heldConfirmation} onOpenChange={open => {if (!open && !state.busy) closeConfirmation(heldConfirmation);}} title={t(heldConfirmation?.kind === 'rotate' ? 'api.webhooks.rotateTitle' : 'api.webhooks.removeTitle', {name:heldConfirmation?.hook.name ?? ''})} description={t(heldConfirmation?.kind === 'rotate' ? 'api.webhooks.rotateBody' : 'api.webhooks.removeBody')} width={460} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy}
      footer={<><Button size="compact" disabled={state.busy} onClick={() => closeConfirmation(heldConfirmation)}>{t('action.cancel')}</Button><Button size="compact" className={styles.danger} disabled={!writable || (heldConfirmation?.kind === 'rotate' && rotateSecret !== '' && !webhookSecretOk(rotateSecret))} onClick={() => {void confirm();}}>{t(heldConfirmation?.kind === 'rotate' ? 'api.webhooks.rotate' : 'web.missingTitles.removeConfirm')}</Button></>}>{heldConfirmation?.kind === 'rotate' && <div className={styles.form}><TextField label={t('api.webhooks.secret')} description={t('api.webhooks.secretHelp')} type="password" autoComplete="new-password" value={rotateSecret} maxLength={256} disabled={state.busy || state.uncertain} error={rotateSecret !== '' && !webhookSecretOk(rotateSecret) ? t('api.webhooks.secretProblem') : undefined} onChange={event => {if (confirmationRef.current === heldConfirmation) setRotateSecret(event.target.value);}}/></div>}{state.error === 'write' && <p role="alert">{t('error.unconfirmedChange')}</p>}</Dialog>
    <Dialog open={!!heldCard} onOpenChange={open => {if (!open && !heldCard?.issued.secret) closeCard();}} title={t('api.webhooks.secretTitle')} width={500} initialFocusRef={secret} dismissOnBack={false} dismissOnOutsidePress={false} showCloseButton={false} footer={<Button size="compact" variant="primary" onClick={closeCard}>{t('web.access.copiedIt')}</Button>}>
      <div className={styles.form}><p className={styles.warning}>{t('api.webhooks.secretShownOnce')}</p><textarea ref={secret} className={styles.secret} readOnly rows={3} value={heldCard?.issued.secret ?? ''} aria-label={t('api.webhooks.secret')} onFocus={event => event.currentTarget.select()}/><div className={styles.actions}><Button size="xs" icon="i-copy" onClick={() => {void copy();}}>{t('web.twoStep.copy')}</Button><span role="status" className={styles.copied}>{copied && t('api.credential.copied')}</span></div></div>
    </Dialog>
    <Dialog open={!!heldRow} onOpenChange={open => {if (!open) closeRow(heldRow);}} title={t('api.webhooks.actions', {name: heldRow?.hook.name ?? ''})} width={420}
      footer={<Button size="compact" variant="glass" onClick={() => closeRow(heldRow)}>{t('action.cancel')}</Button>}>
      <div className={styles.rowActions}>{heldRow && rowActions.map(item => <Button key={item.kind} size="small" variant="text" icon={item.icon} className={item.danger ? styles.rowActionDanger : styles.rowAction} onClick={() => rowAction(heldRow, item.kind)}>{t(item.label)}</Button>)}</div>
    </Dialog>
    <Dialog open={live && !!log} onOpenChange={open => {if (!open) source.close();}} title={t('api.webhooks.log')} description={log?.name} width={720} footer={<Button size="compact" onClick={() => source.close()}>{t('action.done')}</Button>}>
      <div className={styles.form}>{state.deliveries?.items.map(delivery => <div className={styles.delivery} key={delivery.id}><b>{webhookEventLabel(t, defaultI18n.has, delivery.event)}</b><span>{defaultI18n.dateTime(delivery.createdAt)}</span><span>{t(`api.webhooks.delivery.${delivery.state}`)}</span><small>{deliveryProgress(t, delivery, defaultI18n.time)}</small>{deliveryProblem(t, defaultI18n.has, delivery) && <p>{deliveryProblem(t, defaultI18n.has, delivery)}</p>}</div>)}{state.deliveries && !state.deliveries.items.length && <p>{t('api.webhooks.logEmpty')}</p>}
      <div className={styles.pager}><Button size="xs" disabled={busy} onClick={() => {if (log && isCurrent()) void source.select(log);}}>{t('action.refresh')}</Button>{(logCursors.length > 1 || state.deliveries?.nextCursor) && <><Button size="xs" disabled={busy || logCursors.length < 2} onClick={() => {const next = logCursors.slice(0,-1);void logPage(next[next.length-1] ?? '', next);}}>{t('web.pager.previous')}</Button><Button size="xs" disabled={busy || !state.deliveries?.nextCursor} onClick={() => {const next = state.deliveries?.nextCursor;if (next) void logPage(next,[...logCursors,next]);}}>{t('web.pager.next')}</Button></>}</div></div>
    </Dialog>
  </section>;
}
