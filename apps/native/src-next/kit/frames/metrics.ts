import {createContext, useContext} from 'react';
import {useWindowDimensions} from 'react-native';
import {frameSpec, type FrameForm} from '@portico/design';
import {useTheme} from '../theme';
import {usePageWidth} from '../layout/page-inset';

export type FrameMetrics = Readonly<{
  form: FrameForm;
  /** The width the shell gives this page (the window less a tablet's sidebar). */
  width: number;
  /** Rule 4. The left gutter is the theme's; TV's right edge is narrower than its left. */
  gutter: number; rightGutter: number;
  /** Where a grouped card starts, and the padding and corners of one. */
  cardInset: number; cardPadding: number; cardRadius: number;
  /** Rule 5. */
  sectionGap: number; headingGap: number;
  /** R5, a row of cards: one row's captions to the next row's heading, and a heading to its cards. */
  rowSection: number; rowHeadingGap: number;
  headerGap: number; titleTop: number; bottomClear: number; rowTwoLine: number;
}>;

/** Set by a frame around a column whose edges it has already placed (a settings page beside its list): inside it the
 * gutter and card inset are zero, so parts line up with the column instead of the window. */
export const FrameColumn = createContext(false);

/** R5: set by a frame round its rows of cards. Inside it a kit `Row` adds no spacing of its own (it takes back the room
 * its track keeps for a card's growth) and the frame's `rowSection` and `rowHeadingGap` are the whole rhythm. */
export const FrameRhythm = createContext(false);

/** Set by `TitleFrame` on touch: scrolls the page so a part's top sits just under the compact bar and pinned tabs (an
 * episode opened on arrival). Absent outside a title frame and on TV, which moves its page with focus. */
export const FrameReveal = createContext<((node: unknown) => void) | undefined>(undefined);

/** The system rules' spacing for this window: the theme's gutter and packages/design `frameSpec`. Only frames and kit
 * parts call this; a screen has no use for it, because a screen types no spacing of its own. Type is `theme.type`. */
export function useFrameMetrics(): FrameMetrics {
  const theme = useTheme(), width = usePageWidth(), column = useContext(FrameColumn);
  // The form is the theme's (R2: one test, `frameForm`): a tablet's page beside its sidebar is still that tablet's form.
  const form = theme.form, rhythm = frameSpec.rhythm;
  const gutter = column ? 0 : theme.gutter;
  return {form, width, gutter, rightGutter: column ? 0 : form === 'tv' ? theme.layout.gutter.tvRight : theme.gutter,
    cardInset: column ? 0 : form === 'phone' ? frameSpec.card.phoneInset : gutter, cardPadding: frameSpec.card.padding[form], cardRadius: frameSpec.card.radius[form],
    sectionGap: rhythm.section[form], headingGap: rhythm.headingGap[form], rowSection: rhythm.rowSection[form], rowHeadingGap: rhythm.rowHeadingGap[form], headerGap: rhythm.headerGap[form], titleTop: rhythm.titleTop[form],
    bottomClear: rhythm.bottomClear[form], rowTwoLine: rhythm.rowTwoLine[form]};
}

/** R8: the video player's upright form is a window taller than wide (a phone, a tablet in portrait). The picture is
 * then a 16:9 strip with the watch page under it. An open foldable is wider than tall, so it gets the full-window
 * player (foldable 3), as a tablet on its side does. */
export function usePlayerUpright(): boolean {
  const theme = useTheme(), window = useWindowDimensions();
  return !theme.isTV && window.width / window.height < frameSpec.player.upright.maxAspect;
}
