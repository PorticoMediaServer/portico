import type {ReactNode} from 'react';

/** The Settings route supplies its existing revisioned logs-form rows here.
 * This presentation boundary owns no settings state, reads or writes. */
export function MessageOptions({children}: {children: ReactNode}) {
  return <section data-message-options>{children}</section>;
}
