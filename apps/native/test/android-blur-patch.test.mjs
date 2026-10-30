import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {cpSync, mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {patchAndroidBlur} from '../scripts/patch-android-blur.mjs';

const app = fileURLToPath(new URL('../', import.meta.url));
const require = createRequire(import.meta.url);
const source = process.env.PORTICO_BLUR_SOURCE ?? dirname(require.resolve('@react-native-community/blur/package.json'));
const patch = JSON.parse(readFileSync(new URL('../scripts/patches/react-native-blur-4.4.1-android-window.json', import.meta.url), 'utf8'));
const hash = data => createHash('sha256').update(data).digest('hex');
const snapshot = dir => Object.fromEntries(readdirSync(dir, {recursive: true}).sort()
  .filter(path => statSync(join(dir, path)).isFile())
  .map(path => [path, hash(readFileSync(join(dir, path)))]));

function fixture(t, installed = true) {
  const root = mkdtempSync(join(tmpdir(), 'portico-android-blur-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  for (const path of ['package.json', 'package-lock.json']) {
    cpSync(join(app, path), join(root, path));
  }
  const dependency = join(root, 'node_modules', patch.package);
  if (installed) {
    mkdirSync(dirname(dependency), {recursive: true});
    cpSync(source, dependency, {recursive: true});
  }
  return {root, dependency};
}

test('controlled-copy Android patch preserves every other package byte and reruns without writes', t => {
  const {root, dependency} = fixture(t);
  const before = snapshot(dependency);
  assert.equal(patchAndroidBlur(root), patch.files.filter(file => before[file.path] === file.beforeSHA256).length);
  const after = snapshot(dependency);
  assert.deepEqual(Object.keys(after), Object.keys(before));
  for (const [path, sha] of Object.entries(before)) {
    assert.equal(after[path], patch.files.find(file => file.path === path)?.afterSHA256 ?? sha, path);
  }
  const times = patch.files.map(file => statSync(join(dependency, file.path)).mtimeMs);
  assert.equal(patchAndroidBlur(root), 0);
  assert.deepEqual(patch.files.map(file => statSync(join(dependency, file.path)).mtimeMs), times);
});

for (const target of ['package.json', 'package-lock.json', 'installed']) {
  test(`rejects unknown version in ${target} without changing any source`, t => {
    const {root, dependency} = fixture(t);
    const path = target === 'installed' ? join(dependency, 'package.json') : join(root, target);
    const data = JSON.parse(readFileSync(path, 'utf8'));
    if (target === 'package.json') {
      data.dependencies[patch.package] = '4.4.2';
    } else if (target === 'package-lock.json') {
      data.packages[`node_modules/${patch.package}`].version = '4.4.2';
    } else {
      data.version = '4.4.2';
    }
    writeFileSync(path, JSON.stringify(data));
    const before = snapshot(dependency);
    assert.throws(() => patchAndroidBlur(root), /must pin|unsupported installed package/);
    assert.deepEqual(snapshot(dependency), before);
  });
}

test('unknown source in the last file aborts before any file changes', t => {
  const {root, dependency} = fixture(t);
  const path = join(dependency, patch.files.at(-1).path);
  writeFileSync(path, `${readFileSync(path, 'utf8')}\n// unexpected edit\n`);
  const before = snapshot(dependency);
  assert.throws(() => patchAndroidBlur(root), /unexpected source SHA-256/);
  assert.deepEqual(snapshot(dependency), before);
});

test('missing installed dependency fails closed', t => {
  const {root} = fixture(t, false);
  assert.throws(() => patchAndroidBlur(root), /Cannot find module/);
});

test('npm hook preserves both blur patches before the guarded Reader patch', t => {
  const {root, dependency} = fixture(t);
  const scripts = join(root, 'scripts');
  mkdirSync(scripts);
  for (const file of ['patch-apple-blur.mjs', 'patch-android-blur.mjs']) cpSync(join(app, 'scripts', file), join(scripts, file));
  cpSync(join(app, 'scripts/patches'), join(scripts, 'patches'), {recursive: true});
  const manifest = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  assert.deepEqual(manifest.scripts.postinstall.split(' && '), [
    'node scripts/patch-apple-blur.mjs',
    'node scripts/patch-android-blur.mjs',
    'node scripts/patch-android-reader.mjs',
  ]);
  const result = spawnSync(process.execPath, [join(scripts, 'patch-android-blur.mjs')], {cwd: scripts, encoding: 'utf8'});
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /window-root patch verified/);
  assert.equal(patchAndroidBlur(root), 0);
  for (const file of patch.files) assert.equal(hash(readFileSync(join(dependency, file.path))), file.afterSHA256);
});

// Runs the exact patched manager against a small window/lifecycle test double.
// This checks selection/state behavior, not Android pixels or platform rendering.
test('patched Java manager binds the attached window and preserves props through detach/reparent', t => {
  const {root, dependency} = fixture(t);
  patchAndroidBlur(root);
  const files = {
    'android/content/Context.java': 'package android.content; public class Context {}',
    'android/R.java': 'package android; public class R { public static class id { public static final int content=16908290; } }',
    'android/graphics/drawable/Drawable.java': 'package android.graphics.drawable; public class Drawable {}',
    'android/view/View.java': `package android.view;
      import android.content.Context; import android.graphics.drawable.Drawable;
      public class View {
        public View root=this; public Drawable background=new Drawable(); public boolean hardware=true;
        public View(Context c) {} public View getRootView(){return root;}
        public Drawable getBackground(){return background;}
        public <T extends View> T findViewById(int id){return null;}
        public boolean isHardwareAccelerated(){return hardware;} public void invalidate(){}
        protected void onAttachedToWindow(){} protected void onDetachedFromWindow(){}
        public void attach(View window){root=window;onAttachedToWindow();}
        public void detach(){onDetachedFromWindow();root=this;}
      }`,
    'android/view/ViewGroup.java': `package android.view; import android.content.Context;
      public class ViewGroup extends View {
        public ViewGroup content; public ViewGroup(Context c){super(c);}
        @SuppressWarnings("unchecked") public <T extends View> T findViewById(int id){return (T)content;}
      }`,
    'com/facebook/react/uimanager/ThemedReactContext.java': 'package com.facebook.react.uimanager; public class ThemedReactContext extends android.content.Context {}',
    'javax/annotation/Nonnull.java': 'package javax.annotation; public @interface Nonnull {}',
    'eightbitlab/com/blurview/BlurViewFacade.java': `package eightbitlab.com.blurview; import android.graphics.drawable.Drawable;
      public interface BlurViewFacade { BlurViewFacade setFrameClearDrawable(Drawable d); BlurViewFacade setBlurRadius(float r); }`,
    'eightbitlab/com/blurview/BlurView.java': `package eightbitlab.com.blurview;
      import android.content.Context; import android.view.ViewGroup; import android.graphics.drawable.Drawable;
      public class BlurView extends ViewGroup implements BlurViewFacade {
        public ViewGroup capture; public Drawable clear; public float radius; public int color, setups, destroys;
        public boolean auto, enabled=true;
        public BlurView(Context c){super(c);}
        public BlurViewFacade setupWith(ViewGroup root){if(capture!=null){auto=false;destroys++;}capture=root;setups++;return this;}
        public BlurViewFacade setFrameClearDrawable(Drawable value){clear=value;return this;}
        public BlurViewFacade setBlurRadius(float value){radius=value;return this;}
        public BlurViewFacade setOverlayColor(int value){color=value;return this;}
        public BlurViewFacade setBlurAutoUpdate(boolean value){auto=value;return this;}
        public BlurViewFacade setBlurEnabled(boolean value){enabled=value;auto=value;return this;}
        @Override protected void onAttachedToWindow(){if(hardware)auto=true;}
        @Override protected void onDetachedFromWindow(){auto=false;}
      }`,
    'com/reactnativecommunity/blurview/WindowHarness.java': `package com.reactnativecommunity.blurview;
      import android.view.ViewGroup; import eightbitlab.com.blurview.BlurView;
      import com.facebook.react.uimanager.ThemedReactContext;
      public class WindowHarness {
        static void check(boolean value,String message){if(!value)throw new AssertionError(message);}
        public static void main(String[] args){
          ThemedReactContext context=new ThemedReactContext();
          ViewGroup activity=new ViewGroup(context);activity.content=new ViewGroup(context);
          ViewGroup modal=new ViewGroup(context);modal.content=new ViewGroup(context);
          BlurView view=BlurViewManagerImpl.createViewInstance(context);
          check(view.capture==null,"must not bind Activity before actual attachment");
          BlurViewManagerImpl.setRadius(view,16);BlurViewManagerImpl.setColor(view,0xa3100c0c);
          BlurViewManagerImpl.setAutoUpdate(view,false);view.attach(modal);
          check(view.capture==modal.content,"modal must capture modal content");
          check(view.clear==modal.background,"clear drawable must belong to same window");
          check(view.radius==16 && view.color==0xa3100c0c,"existing radius/overlay preserved");
          check(!view.auto && view.enabled,"explicit autoUpdate=false survives attachment");
          BlurViewManagerImpl.setAutoUpdate(view,true);check(view.auto,"live auto update true");
          BlurViewManagerImpl.setBlurEnabled(view,false);check(!view.auto&&!view.enabled,"disabled stays disabled");
          view.detach();check(!view.auto,"detach retires observer");
          BlurViewManagerImpl.setRadius(view,7);view.attach(activity);
          check(view.capture==activity.content && view.clear==activity.background,"reparent selects actual Activity window");
          check(view.destroys==1 && view.setups==2,"reparent replaces old controller exactly once");
          check(view.radius==7&&!view.enabled&&!view.auto,"detached prop changes and disabled state preserved");
          BlurViewManagerImpl.setBlurEnabled(view,true);check(view.auto&&view.enabled,"enabled restores saved auto update");
          view.detach();ViewGroup custom=new ViewGroup(context);view.attach(custom);
          check(view.capture==custom,"window without content ID uses its own root");
          view.detach();view.hardware=false;view.attach(modal);check(!view.auto,"software window does not register auto update");
          BlurView other=BlurViewManagerImpl.createViewInstance(context);other.attach(activity);
          check(other.capture==activity.content&&other.radius==10&&other.auto,"ordinary Activity positive control unchanged");
          System.out.println("window selection/lifecycle assertions PASS");
        }
      }`,
  };
  const java = join(root, 'java');
  const manager = 'com/reactnativecommunity/blurview/BlurViewManagerImpl.java';
  files[manager] = readFileSync(join(dependency, patch.files[0].path), 'utf8');
  for (const [path, content] of Object.entries(files)) {
    mkdirSync(dirname(join(java, path)), {recursive: true}); writeFileSync(join(java, path), content);
  }
  const classes = join(root, 'classes'); mkdirSync(classes);
  const compile = spawnSync('javac', ['-d', classes, ...Object.keys(files).map(path => join(java, path))], {encoding: 'utf8'});
  assert.equal(compile.status, 0, compile.stderr);
  const run = spawnSync('java', ['-cp', classes, 'com.reactnativecommunity.blurview.WindowHarness'], {encoding: 'utf8'});
  assert.equal(run.status, 0, run.stderr); assert.match(run.stdout, /assertions PASS/);
});
