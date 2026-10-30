import React, {useEffect, useState} from 'react';

/** Renders nothing until `quietMs` after mount, so fast loads never flash a loading indicator. */
export function Quiet({children, quietMs = 300}: {children: React.ReactNode; quietMs?: number}) {
  const [shown, setShown] = useState(false);
  useEffect(() => {const timer = setTimeout(() => setShown(true), quietMs); return () => clearTimeout(timer);}, [quietMs]);
  return shown ? <>{children}</> : null;
}
