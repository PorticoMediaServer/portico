import {shellType} from '../../../kit/shell/visual';
import type {useTheme} from '../../../kit/theme';

type Theme = ReturnType<typeof useTheme>;

/** tablet.html 865–867: a Server page in the tablet pane uses the generic page's gutters and heading. */
export function settingsPageInset(theme: Theme, compactHeader = false) {
  return compactHeader || theme.isTV ? {paddingHorizontal: 20, paddingBottom: 40}
    : {paddingTop: 46, paddingHorizontal: theme.width < 1024 ? 24 : 32, paddingBottom: 40};
}

export function settingsPageHeading(theme: Theme) {
  return theme.isTV ? shellType(28, 800, 33, -.03) : shellType(40, 800);
}
