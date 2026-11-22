import React from 'react';
import {TVGroupPillView} from '../../../kit/player/TVGroupPillView';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useTVGroupPill} from '../../../pages/shared/player/useTVGroupPill';

/** The TV player's Watch Together pill and its people sheet, drawn only while a live group owner reports a group. */
export function TVGroupPill(props: Parameters<typeof useTVGroupPill>[0]) {
  const view = useTVGroupPill(props);
  return view ? <TVGroupPillView {...view}/> : null;
}
