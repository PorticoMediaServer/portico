import {createContext, useContext} from 'react';
import {useWindowDimensions} from 'react-native';
import {frameForm, frameSpec, type FrameForm} from '@portico/design';
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
  sectionGap: number; headingGap: number; headerGap: number; titleTop: number; bottomClear: number; rowTwoLine: number;
}>;

/** Set by a frame around a column whose edges it has already placed (a settings page beside its list): inside it the
 * gutter and card inset are zero, so parts line up with the column instead of the window. */
export const FrameColumn = createContext(false);

/** The system rules' spacing for this window: the theme's gutter and packages/design `frameSpec`. Only frames and kit
 * parts call this; a screen has no use for it, because a screen types no spacing of its own. Type is `theme.type`. */
export function useFrameMetrics(): FrameMetrics {
  const theme = useTheme(), width = usePageWidth(), column = useContext(FrameColumn);
  // The form follows the window, as the shell's layout does: a tablet's page beside its sidebar is still that tablet's form.
  const form = theme.isTV ? 'tv' : theme.platform === 'phone' ? 'phone' : frameForm(theme.width), rhythm = frameSpec.rhythm;
  const gutter = column ? 0 : theme.gutter;
  return {form, width, gutter, rightGutter: column ? 0 : form === 'tv' ? theme.layout.gutter.tvRight : theme.gutter,
    cardInset: column ? 0 : form === 'phone' ? frameSpec.card.phoneInset : gutter, cardPadding: frameSpec.card.padding[form], cardRadius: frameSpec.card.radius[form],
    sectionGap: rhythm.section[form], headingGap: rhythm.headingGap[form], headerGap: rhythm.headerGap[form], titleTop: rhythm.titleTop[form],
    bottomClear: rhythm.bottomClear[form], rowTwoLine: rhythm.rowTwoLine[form]};
}

/** The video player's upright form: a window too narrow for its height to give the picture the whole of it (a phone, a
 * tablet in portrait, an open foldable). The picture is then a 16:9 strip with the watch page under it. */
export function usePlayerUpright(): boolean {
  const theme = useTheme(), window = useWindowDimensions();
  return !theme.isTV && window.width / window.height < frameSpec.player.portrait.maxAspect;
}
