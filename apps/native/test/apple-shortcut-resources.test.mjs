import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, existsSync} from 'node:fs';
import {nativeOutputs,nativeTables} from '../../../packages/i18n/scripts/native-strings.mjs';
const project=readFileSync(new URL('../ios/Portico.xcodeproj/project.pbxproj',import.meta.url),'utf8');

test('supported iOS floor uses every canonical localized shortcut table through one resource variant group',()=>{
  const outputs=nativeOutputs(nativeTables().tables);
  const shortcutOutputs=[...outputs].filter(([path])=>path.endsWith('/AppShortcuts.strings'));
  assert.equal(shortcutOutputs.length,3);
  const group=/([A-F0-9]{24}) \/\* AppShortcuts\.strings \*\/ = \{\s*isa = PBXVariantGroup;\s*children = \(([\s\S]*?)\);/.exec(project);
  assert.ok(group,'localized shortcut resource group');
  const refs=[...group[2].matchAll(/([A-F0-9]{24}) \/\* ([^*]+) \*\//g)];
  assert.equal(refs.length,3);
  for(const [path,{value}] of shortcutOutputs){
    const relative=path.replace(/^ios\//,'');
    const ref=refs.find(([,id])=>project.includes(`${id} /*`)&&new RegExp(`${id} /\\*[^*]+\\*/ = \\{isa = PBXFileReference;[^\\n]*path = "${relative.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')}";`).test(project));
    assert.ok(ref,path);
    assert.equal(readFileSync(new URL('../'+path,import.meta.url),'utf8'),value,path);
    assert.ok(project.includes(`"$(SRCROOT)/${relative}",`),'generator output '+path);
  }
  const buildFile=new RegExp(`([A-F0-9]{24}) /\\* AppShortcuts\\.strings in Resources \\*/ = \\{isa = PBXBuildFile; fileRef = ${group[1]}`).exec(project);
  assert.ok(buildFile,'resource build file references the variant group');
  const resources=/isa = PBXResourcesBuildPhase;[\s\S]*?files = \(([\s\S]*?)\);/.exec(project);
  assert.ok(resources?.[1].includes(buildFile[1]),'variant group belongs to app resources');
});

test('Apple project retains iOS15.1 and retires unsupported shortcut catalogue references',()=>{
  const floors=[...project.matchAll(/IPHONEOS_DEPLOYMENT_TARGET = ([^;]+);/g)].map(m=>m[1]);
  assert.ok(floors.length>0);assert.ok(floors.every(value=>value==='15.1'));
  assert.ok(!project.includes('AppShortcuts.xcstrings'));
  assert.ok(!existsSync(new URL('../ios/Portico/Strings/AppShortcuts.xcstrings',import.meta.url)));
});
