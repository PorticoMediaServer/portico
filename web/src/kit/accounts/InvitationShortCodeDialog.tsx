import {useEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {ServerShortInvitation} from '@core/direct-account.ts';
import {Button} from '../controls/Button';
import {Dialog} from '../overlays/Dialog';
import {AuthError} from './AuthStage';
import {sameShortCode, ShortCodeIssuer, type ShortCodeRun} from '@core/invitation-short-code-issuer.ts';
import {copyText} from '../../platform/copy-text';
import styles from './invitation-short-code.module.css';

const t = defaultI18n.t;
export type ShortCodeInvitation = Readonly<{id: string; revision: number; state: 'pending' | 'accepted' | 'revoked' | 'expired'}>;
export type ShortCodeRequest = (operationId: string, signal: AbortSignal, isCurrent: () => boolean) => Promise<ServerShortInvitation>;
export type InvitationShortCodeDialogProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  invitation: ShortCodeInvitation;
  request: ShortCodeRequest;
  /** True only for the explicit creation that opened this card. */
  issueOnOpen?: boolean;
  createOperationId?: () => string;
  /** The captured owner/viewer lifetime used for this issuance. */
  isCurrent?: () => boolean;
  returnFocusRef?: RefObject<HTMLElement | null>;
  children?: ReactNode;
}>;

/**
 * The one explicit short-code issuance surface. A code is shown only from the
 * receipt that carries it; a replay without a code stays an absence and never
 * triggers a second operation. New code is the only thing that rotates it.
 */
export function InvitationShortCodeDialog(props: InvitationShortCodeDialogProps) {
  const identity = `${props.invitation.id}/${props.invitation.revision}`;
  return <IssuerDialog key={identity} {...props} identity={identity} />;
}

type IssuerDialogProps = InvitationShortCodeDialogProps & Readonly<{identity: string}>;
function IssuerDialog({open, onOpenChange, request, issueOnOpen = false, createOperationId, isCurrent, returnFocusRef, identity, children}: IssuerDialogProps) {
  const createRef = useRef(createOperationId);
  createRef.current = createOperationId;
  const issuer = useMemo(() => new ShortCodeIssuer(createRef.current), []);
  const state = useSyncExternalStore(issuer.subscribe, issuer.getSnapshot, issuer.getSnapshot);
  const [copied, setCopied] = useState(false), [copyError, setCopyError] = useState<unknown>();
  const alive = useRef(true), openRef = useRef(open), requestRef = useRef(request), isCurrentRef = useRef(isCurrent), issuedCurrent = useRef<(() => boolean) | undefined>(undefined), runRef = useRef<ShortCodeRun>(() => Promise.reject(new Error('invitation_operation_unavailable')));
  const autoIssued = useRef<string | undefined>(undefined);
  openRef.current = open; requestRef.current = request; isCurrentRef.current = isCurrent;
  runRef.current = (operationId, signal) => {
    const ownerCurrent = isCurrentRef.current;
    const captured = () => alive.current && openRef.current && (ownerCurrent?.() ?? true);
    issuedCurrent.current = captured;
    return requestRef.current(operationId, signal, captured);
  };

  useEffect(() => {
    alive.current = true;
    return () => {alive.current = false;issuedCurrent.current = undefined;issuer.dispose();};
  }, [issuer]);
  useEffect(() => {
    if (!open) {issuer.reset();issuedCurrent.current = undefined;setCopied(false);setCopyError(undefined);return;}
    if (!issueOnOpen || autoIssued.current === identity) return;
    autoIssued.current = identity;
    void issuer.issue(true, runRef.current);
  }, [open, identity, issueOnOpen, issuer]);
  useEffect(() => {if (state.phase !== 'ready') {setCopied(false);setCopyError(undefined);}}, [state.phase]);

  const busy = state.phase === 'issuing';
  const copy = async (source: HTMLElement) => {
    const current = issuedCurrent.current;
    if (!current || !current()) return;
    const snapshot = issuer.getSnapshot();
    if (snapshot.phase !== 'ready') return;
    const code = snapshot.code, operationId = snapshot.operationId;
    const same = () => sameShortCode(current, issuer.getSnapshot(), operationId, code);
    if (!same()) return;
    try {
      await copyText(code, source);
      if (same()) setCopied(true);
    } catch (error) {
      if (same()) setCopyError(error);
    }
  };
  const expiry = state.phase === 'ready' || state.phase === 'unavailable'
    ? new Intl.DateTimeFormat(undefined, {dateStyle: 'medium'}).format(new Date(state.expiresAt)) : undefined;
  return <Dialog open={open} onOpenChange={value => {if (!busy) onOpenChange(value);}} title={t('invite.readyTitle')}
    description={t('invite.readyBody')} size="sm" width={children ? 560 : 540} returnFocusRef={returnFocusRef}
    footer={<Button variant="primary" onClick={() => onOpenChange(false)}>{t('action.done')}</Button>}>
    <div className={styles.body} data-phase={state.phase}>
      {children}
      <div className={styles.codeArea} aria-live="polite">
        {state.phase === 'ready' ? <><p className={styles.code}>{state.code}</p>
          <p className={styles.help}>{t('web.access.shortCodeShownOnce')}</p></> :
          state.phase === 'issuing' ? <p className={styles.pending} role="status">{t('web.directJoin.checking')}</p> :
          state.phase === 'unavailable' ? <p className={styles.help}>{t('web.access.shortCodeUnavailable')}</p> :
          state.phase === 'failed' ? <AuthError error={state.error} /> :
          <p className={styles.help}>{t('web.access.shortCodeHelp')}</p>}
      </div>
      {expiry && <p className={styles.expiry}>{t('api.credential.expires', {when: expiry})}</p>}
      {copyError !== undefined && <AuthError error={copyError} />}
      <div className={styles.actions}>
        {state.phase === 'ready' && <Button variant="glass" icon={copied ? 'i-check' : 'i-copy'} onClick={event => void copy(event.currentTarget)} disabled={busy}>{t(copied ? 'web.together.copied' : 'web.access.copyShortCode')}</Button>}
        {state.phase === 'failed' && <Button variant="glass" onClick={() => void issuer.retry(runRef.current)} disabled={busy}>{t('action.tryAgain')}</Button>}
        {!busy && <Button variant="glass" icon="i-plus" onClick={() => void issuer.issue(true, runRef.current)}>{t('web.access.newShortCode')}</Button>}
      </div>
    </div>
  </Dialog>;
}
