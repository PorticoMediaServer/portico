import {defaultI18n, type I18n, type MessageId} from '../../../../../packages/i18n/src';
import {browseNameId} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {sentence} from '../../../../../packages/client-core/src/presentation/content';

/** The row's own canonical vocabulary, named by the translator the caller currently shows.
 * When the catalogue has no word for a capability, browseNameId says so and the caller shows
 * the admitted id as words, as its contract requires; this presentation string is never the
 * sort/filter value itself. */
export function homeBrowseLabel(value: {id: string; labelKey?: string}, i18n: Pick<I18n, 't' | 'has'> = defaultI18n): string {
  const id = browseNameId(value.id, value.labelKey, i18n.has);
  return id ? i18n.t(id as MessageId) : sentence(value.id);
}
