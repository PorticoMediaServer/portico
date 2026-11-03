import {defaultI18n} from '@i18n';
import {presentError} from '@core/presentation/errors.ts';
import {ServicesNotice} from '../../kit/accounts/ServicesAuth';

/**
 * OW-4: the metadata editor's notice. The service publishes a code and status (a conflict, a refused field, access
 * gone, a write the server didn't confirm), and the shared presenter gives its own sentence. It used to go through the
 * Portico Account presenter with an English sentence as the "code", which always read "Something went wrong".
 */
export function MetadataError({error}: Readonly<{error: unknown}>) {
  if (!error) return null;
  const presented = presentError(error, 'detail', {operation: 'save', i18n: defaultI18n});
  return presented.silent ? null : <ServicesNotice tone="error">{presented.body}</ServicesNotice>;
}
