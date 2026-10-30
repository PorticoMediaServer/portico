import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {StateView} from '../../../kit/states';

/** An album or artist is a title: name that task in its own failure state, and
 * offer only the recovery the error actually permits. Never fall back to Home's
 * copy or a Home retry. */
export function MusicErrorView({error, onRetry, style, testID = 'music-load-error', retryAction = true}: {
  error: unknown; onRetry: () => void; style?: React.ComponentProps<typeof StateView>['style']; testID?: string; retryAction?: boolean;
}) {
  const presented = presentError(error, 'detail', {operation: 'load'});
  const retry = presented.action === 'try-again' || presented.action === 'refresh';
  return <StateView variant="inline" testID={testID} title={presented.title} body={presented.body} style={style}
    {...(retry && retryAction ? {primaryAction: {id: 'retry', label: presented.actionLabel ?? defaultI18n.t('action.tryAgain'), onPress: onRetry}} : {})}/>;
}
