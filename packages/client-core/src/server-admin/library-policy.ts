import {analysisSelectionComplete, type AnalysisMatrix, type LibrarySettings} from '../administration.ts';

/** Toggle the real runner operation and preserve its complete dependency chain. */
export function setLibraryAnalysis(settings: LibrarySettings, matrix: AnalysisMatrix, operation: string, enabled: boolean): LibrarySettings {
  const selected = new Set(settings.analysis);
  if (enabled) {
    selected.add(operation);
    for (;;) {
      const missing = analysisSelectionComplete(matrix, [...selected]);
      if (!missing.length) break;
      for (const id of missing) selected.add(id);
    }
  } else {
    selected.delete(operation);
    for (;;) {
      const invalid = matrix.operations.filter(op => selected.has(op.id) && op.requires.some(id => !selected.has(id)));
      if (!invalid.length) break;
      for (const op of invalid) selected.delete(op.id);
    }
  }
  const next={...settings, analysis:[...selected]};
 return {...next,scanDepth:operation==='trickplay'||operation==='chapter_images'?settings.scanDepth:libraryScanDepth({...next,scanDepth:'custom'}), navigation: {...settings.navigation, chapterThumbnailMode: selected.has('chapter_images') ? 'generated' : 'embedded'}};
}

const coreSteps=['local_metadata','subtitle_discovery','checksum','waveform','loudness','audio_fingerprint'] as const;
/** Exact six-step preset; independent picture flags keep their own choices. */
export function setLibraryScanDepth(settings:LibrarySettings,depth:'file_list_only'|'basic'|'complete'):LibrarySettings{
 const selected=new Set(settings.analysis);
 for(const op of coreSteps)selected.delete(op);
 if(depth!=='file_list_only'){
  selected.add('probe');selected.add('local_metadata');selected.add('subtitle_discovery');
  if(depth==='complete'){selected.add('waveform');selected.add('loudness');}
 }
 return {...settings,scanDepth:depth,analysis:[...selected]};
}
export function libraryScanDepth(settings:LibrarySettings):LibrarySettings['scanDepth']{
 if(settings.scanDepth==='file_list_only')return 'file_list_only';
 const selected=new Set(settings.analysis);
 if(!selected.has('probe'))return 'custom';
 const base=selected.has('local_metadata')&&selected.has('subtitle_discovery')&&!selected.has('checksum')&&!selected.has('audio_fingerprint');
 if(base&&!selected.has('waveform')&&!selected.has('loudness'))return 'basic';
 if(base&&selected.has('waveform')&&selected.has('loudness'))return 'complete';
 return 'custom';
}
/** The stored tile budget is derived from an outcome in hours, capped by the runner. */
export function setLibraryPreviewHours(settings:LibrarySettings,hours:1|2|3|4|6):LibrarySettings{
 if(![1,2,3,4,6].includes(hours))throw new Error('Invalid preview duration.');
 const seconds=settings.navigation.trickplayIntervalMs/1000;
 if(!Number.isInteger(seconds)||seconds<1)throw new Error('Invalid preview spacing.');
 return {...settings,navigation:{...settings.navigation,trickplayMaxTiles:Math.min(4096,Math.floor(hours*3600/seconds))}};
}
export function setLibrarySeekPreviews(settings:LibrarySettings,preset:'off'|'standard'|'smooth'|'compact'):LibrarySettings{
 const selected=new Set(settings.analysis);
 if(preset==='off')selected.delete('trickplay');else {selected.add('probe');selected.add('trickplay');}
 const interval=preset==='smooth'?5000:preset==='compact'?20000:10000;
 return {...settings,analysis:[...selected],navigation:{...settings.navigation,...(preset==='off'?{}:{trickplayIntervalMs:interval,trickplayTileWidth:preset==='compact'?240:320})}};
}
