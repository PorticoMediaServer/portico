import {useEffect, useRef, useState} from 'react';
import type {ReadingListMembership, ReadingPeriodicalIssue} from '../../../../../../packages/client-core/src';
import type {NativeReadingActionOffer} from '../../../screens/read/reading-actions';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {ReadingActionsPort} from './useReadingActions';

export function usePeriodicalActions(issue: ReadingPeriodicalIssue, target: NativeReadingActionOffer | undefined,
  actions: ReadingActionsPort['actions'], reportError: NativeShellRuntime['reportError']) {
  const [personal, setPersonal] = useState<Readonly<{offer: NativeReadingActionOffer; listed: ReadingListMembership}>>();
  const [busy, setBusy] = useState(false);
  const latestTarget = useRef(target);
  latestTarget.current = target;
  useEffect(() => {
    const target = latestTarget.current;
    if (!target) return;
    let active = true;
    setPersonal(undefined);
    void actions.inspect(target).then(listed => {if (active && listed) setPersonal({offer: target, listed});})
      .catch(error => {if (active && target.lifetime.isCurrent()) reportError(error);});
    return () => {active = false;};
  }, [actions, issue.item.id, target?.lifetime.signal, reportError]);
  const listed = personal?.offer.item.id === issue.item.id && personal.offer.lifetime.isCurrent() ? personal : undefined;
  const toggleListed = () => {
    if (!listed || !listed.offer.lifetime.isCurrent()) return;
    setBusy(true);
    void actions.toggleListed(listed.offer, listed.listed).then(value => {
      if (value && listed.offer.lifetime.isCurrent()) setPersonal({offer: listed.offer, listed: value});
    }).catch(reportError).finally(() => setBusy(false));
  };
  return {target, listed, busy, toggleListed};
}
