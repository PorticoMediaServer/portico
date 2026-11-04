import {defaultI18n} from '@i18n';
import {StateView} from '../../kit/states';
import {publicationTooLarge, readingServerBusy, readingSessionEnded} from './open-failure';

const {t} = defaultI18n;
/** Why a book couldn't be opened or read on: the server is busy (RD-8), it ended the session
 * (RD-7), the book is too large to read (RD-5), or anything else. */
export type ReadFailureKind = 'failed' | 'busy' | 'ended' | 'tooLarge';
export const readFailureKind = (error: unknown): ReadFailureKind =>
  readingSessionEnded(error) ? 'ended' : readingServerBusy(error) ? 'busy' : publicationTooLarge(error) ? 'tooLarge' : 'failed';
/** Busy, ended and too large explain themselves on the failed page; only another failure is also reported. */
export const readFailureReported = (kind: ReadFailureKind) => kind === 'failed';

/** The reader's failed page, shared by the reader, the book page's Read and the cover picker:
 * busy says so with Try again; an ended session says so with Open again; a book too large to read
 * says so with only Back (trying again would only be refused again). */
export function ReadOpenFailure({kind, page = true, onRetry, onBack}: Readonly<{kind: ReadFailureKind; page?: boolean; onRetry: () => void; onBack?: () => void}>) {
  const back = onBack ? {id: 'back', label: t('next.reader.back'), onPress: onBack} : undefined;
  if (kind === 'tooLarge') return <StateView {...(page ? {variant: 'page' as const} : {})} title={t('next.reader.publicationTooLargeTitle')}
    body={t('next.reader.publicationTooLargeBody')} {...(back ? {primary: back} : {})}/>;
  const ended = kind === 'ended';
  return <StateView {...(page ? {variant: 'page' as const} : {})} title={t(ended ? 'next.reader.sessionEndedTitle' : 'next.states.pageFailed')}
    body={t(ended ? 'next.reader.sessionEndedBody' : kind === 'busy' ? 'error.busy' : 'next.states.pageFailedBody')}
    primary={{id: ended ? 'reopen' : 'retry', label: t(ended ? 'next.reader.openAgain' : 'next.states.tryAgain'), onPress: onRetry}}
    {...(back ? {secondary: back} : {})}/>;
}
