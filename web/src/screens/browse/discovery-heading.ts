import {defaultI18n} from '@i18n';
import type {ContentSection} from '@core/library-content.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';

/**
 * A Discover shelf's heading. The server names it with a shared code; Discover's own continuation
 * shelf is read by its section id instead, because that code (home.row.continueListening) also
 * names Home's Continue Listening row, which keeps its title case beside Continue Watching. The
 * library frames draw "Continue listening" (desktop-listen #books, phone-app music and audiobooks).
 */
export function discoveryHeading(section: Pick<ContentSection, 'id' | 'heading'>): string {
  if (section.id === 'continue_listening') return defaultI18n.t('next.browse.continueListening');
  return serverTextLabel(defaultI18n, {code: section.heading.key, fallback: section.heading.fallback, params: section.heading.params});
}
