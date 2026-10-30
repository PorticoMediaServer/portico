import {useEffect, useRef, useState} from 'react';
import {DeviceEqualizer} from './Equalizer';
import {createBrowserEqualizerPort, type EqualizerPort} from './equalizer-port';

/** Settings and the player edit the renderer's same app/device-local record. A host that
 * already owns the port passes it here so the row's On/Off state and this page share it. */
export function BrowserSettingsEqualizer({layout, onError, port: supplied}: Readonly<{layout: 'settings'; onError(error: unknown): void; port?: EqualizerPort}>) {
  const error = useRef(onError);
  error.current = onError;
  const [created] = useState(() => supplied ? undefined : createBrowserEqualizerPort(value => error.current(value)));
  const port = supplied ?? created!;
  const [sampleRate, setSampleRate] = useState<number>();
  useEffect(() => {
    // A silent context measures the browser's filter rate for the preview. It neither opens
    // an audio source nor supplies an observed DAC/output rate or an applied-EQ fact.
    let context: AudioContext | undefined;
    try { context = new AudioContext(); setSampleRate(context.sampleRate); } catch { /* No invented rate on unsupported browsers. */ }
    return () => { void context?.close().catch(() => {}); };
  }, []);
  return <DeviceEqualizer port={port} layout={layout} sampleRate={sampleRate} reportError={onError} />;
}
