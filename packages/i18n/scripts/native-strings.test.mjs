import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync} from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {nativeText,nativeTables,nativeOutputs,shortcutStrings,androidText,appleLocale,androidFolder,generateNativeStrings} from './native-strings.mjs';

// Expected text is independently copied from the binding system-copy note, not generated.
const ios='Portico looks for servers, TVs and Google Cast devices on your network. Without it, addresses and TV codes still work.';
const tvos='Portico looks for servers and TVs on your network. Without it, addresses and sign-in codes still work.';
const swift=String.raw`
static var typeDisplayRepresentation: TypeDisplayRepresentation = "Title"
static var title: LocalizedStringResource = "Play a title"
static var description = IntentDescription("Plays something from Continue Watching or My List.")
@Parameter(title: "Title", requestValueDialog: IntentDialog("Which title?")) var title: PorticoTitleEntity
static var title: LocalizedStringResource = "Resume watching"
static var description = IntentDescription("Picks up where you left off.")
phrases: ["Resume \(.applicationName)", "Continue watching in \(.applicationName)", "Play \(\.$title) in \(.applicationName)", "Watch \(\.$title) in \(.applicationName)"]
`;
function fixture(t){
 const root=mkdtempSync(path.join(os.tmpdir(),'native-strings-'));
 t.after(()=>rmSync(root,{recursive:true,force:true}));
 const put=(file,value)=>{const p=path.join(root,file);mkdirSync(path.dirname(p),{recursive:true});writeFileSync(p,value);};
 put('ios/Portico/PorticoIntents.swift',swift);
 for(const [platform,text] of [['ios',ios],['tvos',tvos]]){
  put(`${platform}/Portico/Info.plist`,`<plist><dict><key>NSLocalNetworkUsageDescription</key><string>${text}</string></dict></plist>`);
  put(`${platform}/Portico.xcodeproj/project.pbxproj`,'knownRegions = (en, "en-CA", "en-GB", Base, );');
 }
 return {root,put};
}
test('native-safe ICU extraction preserves literal text and only plain arguments',()=>{
 assert.equal(nativeText("Open '{' then {name} and ''close''"),"Open { then {name} and 'close'");
 for(const input of ['{n, number}','{n, plural, one {One} other {Many}}','{x, select, yes {Yes} other {No}}'])assert.equal(nativeText(input),undefined);
});
test('unsafe system text fails while unsafe row titles are skipped consistently across locales',()=>{
 const source={'system.test':'Plain {name}','home.row.safe':'For {name}','home.row.unsafe':'{n, number}'};
 const {tables,skipped}=nativeTables(['en-US','en-GB'],locale=>locale==='en-GB'?{'home.row.safe':'For British {name}'}:{},source);
 assert.deepEqual(skipped,['home.row.unsafe']);assert.equal(tables.get('en-GB')['system.test'],'Plain {name}');
 assert.equal(tables.get('en-GB')['home.row.safe'],'For British {name}');
 assert.throws(()=>nativeTables(['en-US'],()=>({}),{'system.bad':'{n, number}'}),/not native-safe/);
});
test('native resource escaping and locale folders follow the exact platform rules',()=>{
 assert.equal(androidText(`@name & <title> 'word' "yes" \\`),String.raw`\@name &amp; &lt;title> \'word\' \"yes\" \\`);
 assert.equal(appleLocale('en-US'),'en');assert.equal(appleLocale('en-GB'),'en-GB');
 assert.equal(androidFolder('en-US'),'values');assert.equal(androidFolder('en-GB'),'values-en-rGB');assert.equal(androidFolder('fr'),'values-fr');
 assert.throws(()=>androidFolder('en-US-extra'));
});
test('production tables have complete regional keys, exact system words and parameterized Siri resources',()=>{
 const {tables}=nativeTables();const outputs=nativeOutputs(tables);
 const sourceKeys=Object.keys(tables.get('en-US'));
 assert.equal(sourceKeys.filter(k=>k.startsWith('system.')).length,18);
 for(const table of tables.values())assert.deepEqual(Object.keys(table),sourceKeys);
 const siri=JSON.parse(outputs.get('ios/Portico/Strings/Localizable.xcstrings').value);
 assert.equal(siri.strings['Play a title'].localizations.en.stringUnit.value,'Play a title');
 assert.equal(siri.strings['Which title?'].localizations['en-GB'].stringUnit.value,'Which title?');
 assert.equal(outputs.has('ios/Portico/Strings/AppShortcuts.xcstrings'),false);
 const expected=['Continue watching in ${applicationName}','Play ${title} in ${applicationName}','Resume ${applicationName}','Watch ${title} in ${applicationName}'];
 for(const locale of ['en','en-CA','en-GB']){
  const shortcuts=outputs.get(`ios/Portico/Strings/${locale}.lproj/AppShortcuts.strings`).value;
  assert.deepEqual(shortcuts.split('\n').filter(line=>line.startsWith('"')),expected.map(phrase=>`"${phrase}" = "${phrase}";`));
 }
 const android=outputs.get('android/app/src/main/res/values/portico_system_strings.xml').value;
 assert.ok(android.includes('<string name="system_android_download_running" formatted="false">Downloading</string>'));
 assert.ok(android.includes('name="next_home_continueWatching"'));
});
test('invalid phrases and duplicate Apple source text are refused',()=>{
 const source=nativeTables().tables;
 const invalid=new Map([...source].map(([locale,table])=>[locale,{...table,'system.siri.phrase.play':'Play {title}'}]));
 assert.throws(()=>nativeOutputs(invalid),/Invalid Siri phrase/);
 const duplicate=new Map([...source].map(([locale,table])=>[locale,{...table,'system.siri.duplicate':'Title'}]));
 assert.throws(()=>nativeOutputs(duplicate),/Duplicate Apple source text/);
});
test('generator writes complete outputs, check is read-only and reports a stale committed resource',t=>{
 const {root}=fixture(t);const result=generateNativeStrings({nativeRoot:root});
 assert.equal(result.outputs.length,11);assert.equal(generateNativeStrings({nativeRoot:root,check:true}).outputs.length,11);
 const file=path.join(root,'android/app/src/main/res/values/portico_system_strings.xml');
 writeFileSync(file,'stale');assert.throws(()=>generateNativeStrings({nativeRoot:root,check:true}),/Stale native system strings/);assert.equal(readFileSync(file,'utf8'),'stale');
});
test('every run validates genuine Swift/plist/language inputs before any selected platform output',t=>{
 const {root,put}=fixture(t);put('ios/Portico/PorticoIntents.swift',swift.replace('Resume watching','Resume Watching'));
 assert.throws(()=>generateNativeStrings({nativeRoot:root,platform:'android'}),/missing catalogue literal/);
 put('ios/Portico/PorticoIntents.swift',swift);put('tvos/Portico/Info.plist','<plist/>');
 assert.throws(()=>generateNativeStrings({nativeRoot:root}),/Local Network text/);
 put('tvos/Portico/Info.plist',`<key>NSLocalNetworkUsageDescription</key><string>${tvos}</string>`);
 put('ios/Portico.xcodeproj/project.pbxproj','knownRegions = (en, Base, );');
 assert.throws(()=>generateNativeStrings({nativeRoot:root}),/knownRegions is missing/);
});

// Native MAIN e63ed84 PorticoTitleEntity.displayRepresentation uses these
// content-title calls. They are catalogue data, not App Intents system copy.
const contentEntity=String.raw`
struct PorticoTitleEntity: AppEntity {
  var title: String
  var subtitle: String?
  var displayRepresentation: DisplayRepresentation {
    if let subtitle, !subtitle.isEmpty { return DisplayRepresentation(title: "\(title)", subtitle: "\(subtitle)") }
    return DisplayRepresentation(title: "\(title)")
  }
}
`;
test('actual AppEntity content title interpolation is not an uncatalogued system literal',t=>{
 const {root,put}=fixture(t);put('ios/Portico/PorticoIntents.swift',swift+contentEntity);
 assert.equal(generateNativeStrings({nativeRoot:root,platform:'apple'}).outputs.length,7);
 assert.equal(generateNativeStrings({nativeRoot:root,platform:'apple',check:true}).outputs.length,7);
});
test('AppShortcuts strings escape source and translations without changing parameter tokens',()=>{
 const source={'system.siri.phrase.test':'Play "{title}" in {app}'};
 const title='${title}',applicationName='${applicationName}';
 assert.equal(shortcutStrings(source,{'system.siri.phrase.test':'Use \\ "{title}"\n{app}\t\u0001'}),String.raw`/* Generated from packages/i18n; do not edit. */
"Play \"${title}\" in ${applicationName}" = "Use \\ \"${title}\"\n${applicationName}\t\U0001";
`);
 assert.throws(()=>shortcutStrings({...source,'system.siri.phrase.duplicate':source['system.siri.phrase.test']},source),/Duplicate Apple source text/);
});
for(const extra of [
 String.raw`@Parameter(title: "\(title)") var other: String`,
 String.raw`static var title: LocalizedStringResource = "\(title)"`,
 String.raw`static var description = IntentDescription("\(title)")`,
 String.raw`let prompt = IntentDialog("\(title)")`,
 String.raw`let display = DisplayRepresentation(title: "Now playing \(title)")`,
 String.raw`let display = DisplayRepresentation(title: "Uncatalogued title")`,
 String.raw`let display = OtherDisplayRepresentation(title: "\(title)")`,
 String.raw`let display = DisplayRepresentation(title: "\\(title)")`,
 String.raw`let display = DisplayRepresentation(title: "\(title)", subtitle: "Episode")`,
 String.raw`let display = DisplayRepresentation(title: "\(title)", subtitle: "\(title)")`,
])test('content-title handling does not exempt other copy context '+extra.split('=')[0],t=>{
 const {root,put}=fixture(t);put('ios/Portico/PorticoIntents.swift',swift+contentEntity+extra);
 assert.throws(()=>generateNativeStrings({nativeRoot:root,platform:'apple'}),/uncatalogued literal/);
});
