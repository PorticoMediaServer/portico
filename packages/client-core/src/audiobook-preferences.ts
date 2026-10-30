import type {PreferenceSnapshot} from './preferences.ts';

export type AudiobookPreferences = Readonly<{skipBackMs:number;skipForwardMs:number;resumeRewindMs:number}>;
export const defaultAudiobookPreferences:AudiobookPreferences=Object.freeze({skipBackMs:15000,skipForwardMs:30000,resumeRewindMs:0});

/** Missing fields on an older server use the declared defaults. A published
 * unsupported value is refused, never silently turned into another choice. */
export function audiobookPreferences(snapshot:PreferenceSnapshot):AudiobookPreferences {
 const read=(key:string,fallback:number,choices:readonly number[])=>{
  const field=snapshot.registry.fields.find(field=>field.key===key);
  const value=Object.hasOwn(snapshot.effective,key)?snapshot.effective[key]:field!==undefined?field.default:fallback;
  if(typeof value!=='number'||!choices.includes(value))throw new Error('invalid_audiobook_preference');
  return value;
 };
 return Object.freeze({skipBackMs:read('audiobooks.skipBackMs',defaultAudiobookPreferences.skipBackMs,[10000,15000,30000,60000]),skipForwardMs:read('audiobooks.skipForwardMs',defaultAudiobookPreferences.skipForwardMs,[15000,30000,60000,90000]),resumeRewindMs:read('audiobooks.resumeRewindMs',defaultAudiobookPreferences.resumeRewindMs,[0,5000,15000,30000])});
}
