type Transport=Readonly<{
  measure(options:unknown):Promise<unknown>;validate(value:unknown):readonly string[];
  focus():unknown;write(value:unknown):Promise<void>;
}>;
/** Opaque canonical facts. This transport adds no measurements or tolerances. */
export async function collectDesktopMeasurement(options:unknown,host:Transport,signal:AbortSignal){
  signal.throwIfAborted();const focused=host.focus();
  const actual=await host.measure(options),repeated=await host.measure(options);
  signal.throwIfAborted();
  if(host.focus()!==focused||JSON.stringify(actual)!==JSON.stringify(repeated))throw new Error('desktop_measurement_unstable');
  const facts=actual as {fontStatus?:string;fontFaces?:readonly {status?:string}[]}|null;
  // The canonical DOM collector declares CSS units and truthful backing density.
  // Preserve both exactly; logical-unit validation belongs to its shared contract.
  if(host.validate(actual).length||facts?.fontStatus!=='loaded'||
    !facts.fontFaces?.some(face=>face.status==='loaded')||facts.fontFaces.some(face=>face.status==='error'))throw new Error('desktop_measurement_unavailable');
  await host.write(actual);
}
