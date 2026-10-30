import {useCallback, useMemo, useState} from 'react';
import {useWindowHeight, useWindowWidth} from '../../kit/theme';
import {guideViewChoice} from './guide-view-choice';

export type GuideView = 'guide' | 'list';
export type GuideViewScope = Readonly<{serverId: string; viewerId: string; kind: string; sourceId: string}>;
const valid = (value: unknown): value is GuideView => value === 'guide' || value === 'list';

/** A browser view choice, separate from account settings. Only an explicit
 * selection writes; URL choices always take precedence over local memory. */
export function useGuideViewPreference(scope: GuideViewScope, explicit: GuideView | undefined, current: () => boolean) {
  const width = useWindowWidth(), height = useWindowHeight();
  const key = `portico.guide.view.v1:${JSON.stringify([scope.serverId, scope.viewerId, scope.kind, scope.sourceId])}`;
  const stored = useMemo(() => {
    try {
      const value = globalThis.localStorage?.getItem(key);
      return valid(value) ? value : undefined;
    } catch {return undefined;}
  }, [key]);
  const [chosen, setChosen] = useState<{key: string; value: GuideView}>();
  const remember = useCallback((value: GuideView) => {
    if (!current()) return;
    setChosen({key, value});
    try {globalThis.localStorage?.setItem(key, value);} catch { /* Disabled storage keeps the current mounted choice. */ }
  }, [key, current]);
  return {view: guideViewChoice(explicit, chosen?.key === key ? chosen.value : stored, width, height), remember};
}
