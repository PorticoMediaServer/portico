import React from 'react';
import type {ErrorContext} from '../../../../../packages/client-core/src/presentation/errors';
import {StateView} from './StateView';
import {StateLayout} from './StateLayout';
import type {StateContentProps} from './types';
import {useTheme} from '../theme';

/** What a screen's feedback service gives for a failure: the state's words and its retry. */
type ErrorPresenter = Readonly<{error: (error: unknown, retry: () => void, context: ErrorContext) => StateContentProps}>;

/** An inline failure (a panel, a plate): the centred state. */
export function nativeScreenError(service: ErrorPresenter, context: ErrorContext) {
  return (error: unknown, retry: () => void) => <StateView variant="centered" {...service.error(error, retry, context)}/>;
}

/** A screen's whole-page load failure (states.html 13, phone-detail.html): the standard backdrop, the state drawn below
 * the screen's own Back row and clear of the status bar, as the title page's removed and restricted states are. TV keeps
 * its side backdrop with the state centred. Inline places (a panel, a plate) keep nativeScreenError. */
export function nativeScreenErrorPage(service: ErrorPresenter, context: ErrorContext) {
  return (error: unknown, retry: () => void) => <ScreenErrorPage {...service.error(error, retry, context)}/>;
}

/** The full-page form for a screen that renders its own state content (the person page, W-2 sweep). */
export function ScreenErrorPage(props: StateContentProps) {
  const theme = useTheme();
  return <StateLayout {...props} testID={props.testID ?? 'screen-error-page'} contentTop={theme.isTV ? undefined : 250}/>;
}
