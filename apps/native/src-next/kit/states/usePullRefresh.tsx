import React, {useEffect, useRef, useState} from 'react';
import {RefreshControl} from 'react-native';
import {useTheme} from '../theme';

/** motion.html 2.13: the system pull-to-refresh on touch, tinted for ink on both platforms.
 * The ring stays only while `run` is pending; TV and absent work get no control. */
export function usePullRefresh(run: (() => Promise<unknown>) | undefined, onError: (error: unknown) => void, progressViewOffset?: number) {
  const theme = useTheme(), [refreshing, setRefreshing] = useState(false), live = useRef(true), busy = useRef(false);
  useEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  if (theme.isTV || !run) return undefined;
  return <RefreshControl refreshing={refreshing} tintColor={theme.colors.text} colors={[theme.colors.accentStrong]} progressBackgroundColor={theme.colors.raised} progressViewOffset={progressViewOffset}
    onRefresh={() => {
      if (busy.current) return;
      busy.current = true; setRefreshing(true);
      void Promise.resolve().then(run).catch(onError).finally(() => {busy.current = false; if (live.current) setRefreshing(false);});
    }}/>;
}
