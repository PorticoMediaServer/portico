package com.facebook.react.views.modal;

public final class ReactModalHostView {
  private static ReactModalHostView retained;
  private static ReactModalHostView inactive;
  private static ReactModalHostView secondShown;
  private static final class Dialog {private boolean mShowing; Dialog(boolean shown) {mShowing = shown;}}
  private static final class Root {private Object mParent; private Object mAttachInfo; Root(boolean attached) {if (attached) {mParent = new Object(); mAttachInfo = new Object();}}}
  private final Dialog dialog;
  private final Root dialogRootViewGroup;
  private ReactModalHostView(boolean shown, boolean attached) {dialog = new Dialog(shown); dialogRootViewGroup = new Root(attached);}
  private void dismiss() { System.out.println("DISMISSED"); }
  private void onDetachedFromWindow() { dismiss(); }
  public static void main(String[] args) throws Exception {
    retained = new ReactModalHostView(true, true);
    inactive = new ReactModalHostView(false, false);
    if (args.length > 0 && args[0].equals("ambiguous")) secondShown = new ReactModalHostView(true, true);
    System.out.println("FIXTURE_READY");
    System.out.flush();
    if (System.in.read() < 0) throw new IllegalStateException("No trigger");
    inactive.onDetachedFromWindow();
    retained.onDetachedFromWindow();
    // Keep the VM alive until the observer has completed its read/resume/disconnect.
    System.in.read();
  }
}
