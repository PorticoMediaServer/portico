import React from 'react';
import {TitleScreenView} from '../../../kit/player/TitleScreenView';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useTitleScreen, type NativeTitleScreenProps} from '../../../pages/shared/player/useTitleScreen';

export type {NativeTitleScreenProps} from '../../../pages/shared/player/useTitleScreen';

/** The retained title route: the watch page's body and the title page for kinds without a rebuilt page. */
export function TitleScreenPage(props: NativeTitleScreenProps) {
  return <TitleScreenView {...useTitleScreen(props)}/>;
}
