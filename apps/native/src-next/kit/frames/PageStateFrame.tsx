import React from 'react';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {StateLayout, type StateRetry} from '../states/StateLayout';
import type {StateAction, StateTone} from '../states/types';
import type {IconName} from '../Icon';

export type PageStateProps = Readonly<{
  icon: IconName; tone?: StateTone;
  title: string;
  /** One sentence: the flexible region. */
  body?: string;
  primaryAction?: StateAction; secondaryAction?: StateAction;
  /** The countdown to the next automatic try. */
  retry?: StateRetry;
  /** The owner-only Details block. */
  details?: React.ReactNode;
  /** What still works: the kit `DownloadsStillPlay` row, before the buttons (states 4a). */
  downloads?: React.ReactNode;
  /** One paragraph under the buttons ("If the server is asleep…", states 4a, 5). */
  help?: React.ReactNode;
  testID?: string;
  focus?: Pick<FocusRegionProps, 'id' | 'entry' | 'edges'>;
}>;

/** Frame 9: the whole page can't load, or is waiting the first time. Once per screen, in place of the page: the icon
 * in a glass circle, a large title, one sentence, a white primary and a glass secondary; centred on a phone, at the
 * gutter elsewhere. The other frames draw it for their `state`; a screen with no frame of its own uses it directly. */
export function PageStateFrame({downloads, focus, ...props}: PageStateProps) {
  // R1: a page state starts on its primary action (tv-focus 47).
  const primaryAction = props.primaryAction && {...props.primaryAction, preferredFocus: props.primaryAction.preferredFocus ?? true};
  const state = <StateLayout {...props} primaryAction={primaryAction} beforeActions={downloads} style={{backgroundColor: 'transparent'}}/>;
  return focus ? <FocusRegion {...focus} style={{flex: 1}}>{state}</FocusRegion> : state;
}
