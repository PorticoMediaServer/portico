import {useRef} from 'react';
import type {Library} from '@core/index.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {useShellFacts} from '../../shell/fact-hooks';

/** One route's last confirmed presentation. A tolerant directory omission is not
 * removal proof. True Home/authority retirement still preempts this projection. */
export function useRouteLibrary(context: ShellAssemblyContext | undefined, libraryId: string): Library | undefined {
  const facts = useShellFacts(context), held = useRef<Readonly<{context: ShellAssemblyContext; libraryId: string; library: Library}> | undefined>(undefined);
  if (!context?.isCurrent()) return undefined;
  const library = facts?.identity.libraries.find(row => row.id === libraryId);
  if (library) held.current = {context, libraryId, library};
  return library ?? (held.current?.context === context && held.current.libraryId === libraryId ? held.current.library : undefined);
}
