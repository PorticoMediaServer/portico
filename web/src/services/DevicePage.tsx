import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ApiError} from '@core/index.ts';
import {ServicesAccountLine, ServicesAuthStage, ServicesCodeChip} from '../kit/accounts/ServicesAuth';
import {ServicesAuthForm} from './AuthForm';
import {accountIdentity, sameAccount, type AccountIdentity, type ServicesAccount} from './session';
import {decideHostedDevice, reviewHostedDevice, type DeviceReview} from './device-approval';
import {servicesI18n} from './i18n';
import {DeviceDecision, DeviceView} from './DeviceView';

export function DevicePage({account, initialCode = ''}: {account: ServicesAccount; initialCode?: string}) {
  const {t} = useMemo(servicesI18n, []);
  const session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const [authenticating, setAuthenticating] = useState(() => !session);
  useEffect(() => { if (!session) setAuthenticating(true); }, [session]);
  const [code, setCode] = useState(initialCode), [review, setReview] = useState<DeviceReview>();
  const [fromQR, setFromQR] = useState(Boolean(initialCode));
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [done, setDone] = useState<{decision: 'approve' | 'deny'; identity: AccountIdentity}>();
  const binding = review?.identity ?? done?.identity;
  const stale = Boolean(binding && (!session?.familyId || !sameAccount(binding, accountIdentity(session))));
  const operation = useRef<AbortController | null>(null);
  useEffect(() => () => operation.current?.abort(), []);
  useEffect(() => {
    if (stale) {
      operation.current?.abort(); operation.current = null; setBusy(false); setReview(undefined); setDone(undefined);
      setError(new ApiError(409, 'account_changed', 'account_changed', false));
    }
  }, [stale]);
  useEffect(() => {
    if (!review || busy || stale) return;
    const expire = () => {
      // A submitted decision owns its result; only retire an idle preview.
      if (operation.current) return;
      setReview(current => current === review ? undefined : current);
      setError(new ApiError(410, 'expired', 'expired', false));
    };
    const remaining = Date.parse(review.preview.expiresAt) - Date.now();
    if (remaining <= 0) {expire(); return;}
    const timer = setTimeout(expire, Math.min(remaining, 2147483647));
    return () => clearTimeout(timer);
  }, [review, busy, stale]);
  const run = async (work: (signal: AbortSignal) => Promise<void>) => {
    if (operation.current) return;
    const controller = new AbortController(); operation.current = controller; setBusy(true); setError(undefined);
    const timeout = setTimeout(() => controller.abort(new DOMException('timeout', 'TimeoutError')), 15000);
    try { await work(controller.signal); }
    catch (value) {
      if (!controller.signal.aborted) {
        setError(value);
        if ([400, 404, 410, 422].includes((value as {status?: number})?.status ?? 0)) setReview(undefined);
      } else if (controller.signal.reason?.name === 'TimeoutError') {
        setReview(undefined); setError(new ApiError(408, 'timeout', 'timeout', true));
      }
    }
    finally { clearTimeout(timeout); if (operation.current === controller) operation.current = null; if (!controller.signal.aborted || controller.signal.reason?.name === 'TimeoutError') setBusy(false); }
  };
  const inspect = () => run(async signal => { const value = await reviewHostedDevice(account, code, signal); if (!signal.aborted) setReview(value); });
  const decide = (decision: 'approve' | 'deny') => run(async signal => {
    if (!review) return;
    if (Date.parse(review.preview.expiresAt) <= Date.now()) throw new ApiError(410, 'expired', 'expired', false);
    await decideHostedDevice(account, review, decision, signal);
    if (!signal.aborted) {setDone({decision, identity: review.identity}); setReview(undefined);}
  });
  const another = () => {
    if (operation.current) return;
    setCode(''); setFromQR(false); setReview(undefined); setDone(undefined); setError(undefined);
  };
  if (!session || authenticating) return <ServicesAuthStage variant="deviceSignIn"><ServicesAuthForm account={account} onSignedIn={() => setAuthenticating(false)} embedded heading={t('web.device.title')} lead={t('web.device.lede')}
    before={<>{fromQR && code && <ServicesCodeChip label={t('services.device.codeFromTV')} code={code} />}<ServicesAccountLine>{t('services.device.signedOut')}</ServicesAccountLine></>} /></ServicesAuthStage>;
  if (done && !stale) return <DeviceDecision decision={done.decision} onAnother={another} />;
  return <DeviceView name={session.account.displayName || session.account.username} code={code} fromQR={fromQR} preview={review && !stale ? review.preview : undefined}
    busy={busy} error={error} onCodeChange={setCode} onInspect={() => void inspect()} onDecision={decision => void decide(decision)} onAnother={another} onSignOut={() => void account.service.logout().catch(setError)} />;
}
