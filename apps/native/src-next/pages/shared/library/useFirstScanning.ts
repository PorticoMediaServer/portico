import {useEffect, useState} from 'react';
import type {NativeLibraryFirstScanPort} from '../../../screens/library/first-scan';

export function useFirstScanning(port: NativeLibraryFirstScanPort | undefined, libraryId: string, enabled: boolean): boolean {
  const [scanning, setScanning] = useState(false);
  useEffect(() => {
    setScanning(false);
    return port && enabled ? port.observe(libraryId, setScanning) : undefined;
  }, [port, libraryId, enabled]);
  return enabled && scanning;
}
