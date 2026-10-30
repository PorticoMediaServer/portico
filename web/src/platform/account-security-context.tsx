import {createContext, useContext, type ReactNode} from 'react';
import type {OriginalProviderSecurity} from './account-security';

const original = createContext<OriginalProviderSecurity | undefined>(undefined);
export function OriginalAccountSecurityContext({value, children}: {value: OriginalProviderSecurity; children: ReactNode}) {
  return <original.Provider value={value}>{children}</original.Provider>;
}
export const useOriginalAccountSecurity = () => useContext(original);
