import {useLayoutEffect, useRef, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {AccessMember} from '@core/server-administration.ts';
import type {MemberResetCodeSheetProps} from '../../kit/settings/MemberResetCodeSheet';
import {router} from '../../router';
import {useOwnedResource} from './remote-sources/resources';
import {MemberResetCodePanel, type MemberResetCodeSource} from './member-reset-code';

const noSubscribe = () => () => {};
const noState = () => undefined;
const notBusy = () => false;
const t = defaultI18n.t;

/** Props only fence the original committed panel; no latest identity is adopted.
 * The purpose-specific panel owns each rendered screen and Make/result opening. */
export function useMemberResetCodeView(source: MemberResetCodeSource | undefined, person: AccessMember | undefined,
  isCurrent: () => boolean, blocked: boolean, reportError: (error: unknown) => void, viewKey?: unknown) {
  const inputs = useRef({source, person, isCurrent, blocked, viewKey});
  inputs.current = {source, person, isCurrent, blocked, viewKey};
  const owned = useOwnedResource(() => {
    const location = router.state.location;
    const panel = new MemberResetCodePanel(source, person, () => router.state.location === location &&
      inputs.current.source === source && inputs.current.person === person && inputs.current.isCurrent === isCurrent &&
      inputs.current.viewKey === viewKey && isCurrent(), () => !inputs.current.blocked);
    const off = router.subscribe('onBeforeNavigate', panel.retire);
    return {panel, reportError, retire: () => {panel.retire();off();}};
  }, entry => entry.retire(), [source, person, isCurrent, viewKey, router.state.location]);
  const panel = owned?.value.panel;
  useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? notBusy, notBusy);
  const screen = useSyncExternalStore(panel?.subscribe ?? noSubscribe, panel?.getSnapshot ?? noState, noState);
  const opening = screen?.kind === 'open' ? screen.opening : undefined;
  const state = useSyncExternalStore(opening?.subscribe ?? noSubscribe, opening?.getSnapshot ?? noState, noState);
  useLayoutEffect(() => {if (blocked && panel && screen?.kind === 'open') panel.done(screen);}, [blocked, panel, screen]);
  useLayoutEffect(() => {
    if (!panel?.current() || !opening || opening.getSnapshot() !== state || state?.kind !== 'unresolved') return;
    const error = state.error, status = error && typeof error === 'object' && 'status' in error ? error.status : undefined;
    if ((status === 401 || status === 403) && screen && panel.leave(screen)) owned?.value.reportError(error);
  }, [panel, opening, state, screen, owned?.value]);
  if (!panel?.current() || !screen) return undefined;
  let sheet: MemberResetCodeSheetProps | undefined;
  if (opening?.current() && state && state.kind !== 'retired') {
    sheet = {open: true, username: opening.target.username,
      result: state.kind === 'ready' && opening.unexpired(state) ? {state: 'ready', code: state.result.code, expiresAt: state.result.expiresAt,
        ...(state.error ? {error: t('next.states.pageFailedBody')} : {})}
        : state.kind === 'pending' ? {state: 'pending'} : {state: 'unresolved', error: t('next.states.pageFailedBody')},
      turnOffTwoStep: state.turnOffTwoStep, busy: state.kind === 'pending',
      onTurnOffTwoStepChange: value => {if (panel.current() && panel.getSnapshot() === screen && !inputs.current.blocked) void opening.change(state, value);},
      onCopy: () => {if (panel.current() && panel.getSnapshot() === screen && !inputs.current.blocked) void opening.copy(state);},
      onDone: () => panel.done(screen)};
  }
  return {present: panel.present(), canMake: panel.canMake(screen), begin: () => panel.begin(screen), leave: () => panel.leave(screen), sheet};
}
