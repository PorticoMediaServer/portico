import {createContext, useContext, useMemo} from 'react';
import type {TextStyle} from 'react-native';
import {frameForm, frameSpec, type FrameForm, type FrameTypeRole} from '@portico/design';
import {nativeType, useTheme} from '../theme';
import {usePageWidth} from '../layout/page-inset';

export type FrameMetrics = Readonly<{
  form: FrameForm;
  /** The width the shell gives this page (the window less a tablet's sidebar). */
  width: number;
  gutter: number; rightGutter: number;
  /** Where a grouped card starts, and the padding inside it (rule 4). */
  cardInset: number; cardPadding: number; cardRadius: number;
  /** Rule 5. */
  sectionGap: number; headingGap: number; headerGap: number; titleTop: number; bottomClear: number; rowTwoLine: number;
  /** Rule 6: one style per role. */
  type: Readonly<Record<FrameTypeRole, TextStyle>>;
}>;

/** Set by a frame around a column whose edges it has already placed (a settings page beside its list): inside it the
 * gutter and card inset are zero, so parts line up with the column instead of the window. */
export const FrameColumn = createContext(false);

/** The system rules' numbers for this window, from packages/design `frameSpec`. Only frames and kit parts call this;
 * a screen has no use for it, because a screen types no spacing or sizes of its own. */
export function useFrameMetrics(): FrameMetrics {
  const theme = useTheme(), width = usePageWidth(), column = useContext(FrameColumn);
  // The form follows the window, as the shell's layout does: a tablet's page beside its sidebar is still that tablet's form.
  const form = theme.isTV ? 'tv' : theme.platform === 'phone' ? 'phone' : frameForm(theme.width);
  const textScale = theme.textScale;
  return useMemo(() => {
    const scale = form === 'tv' ? 'tv' : 'handheld', spec = frameSpec, rhythm = spec.rhythm;
    const type = Object.fromEntries(Object.entries(spec.type).map(([name, value]) => [name, nativeType(value[scale], textScale)])) as Record<FrameTypeRole, TextStyle>;
    return {form, width, gutter: column ? 0 : spec.gutter[form], rightGutter: column ? 0 : form === 'tv' ? spec.gutter.tvRight : spec.gutter[form],
      cardInset: column ? 0 : spec.card.inset[form], cardPadding: spec.card.padding[form], cardRadius: spec.card.radius[form],
      sectionGap: rhythm.section[form], headingGap: rhythm.headingGap[form], headerGap: rhythm.headerGap[form], titleTop: rhythm.titleTop[form],
      bottomClear: rhythm.bottomClear[form], rowTwoLine: rhythm.rowTwoLine[form], type};
  }, [form, width, textScale, column]);
}
