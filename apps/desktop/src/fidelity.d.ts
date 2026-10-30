declare module 'virtual:portico-desktop-fidelity' {
  export function measureScreen(options:unknown):Promise<unknown>;
  export function validateMeasurement(value:unknown):string[];
}
