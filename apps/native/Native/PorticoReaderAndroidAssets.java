package com.reactnativecommunity.webview;

import android.content.Context;
import android.net.Uri;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import java.io.ByteArrayInputStream;
import java.io.FilterInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

/** Immutable APK assets only. There is no filesystem, server or book path port. */
public final class PorticoReaderAndroidAssets {
  public static final String ENTRY = "https://appassets.androidplatform.net/PorticoReader.bundle/index.html";
  private static final String PREFIX = "/PorticoReader.bundle/";
  private static final long LIMIT = 64L * 1024 * 1024;
  public static boolean isEntry(String url) { return ENTRY.equals(url); }
  public static boolean isOrigin(String url) {
    Uri uri = Uri.parse(url);
    return "https".equals(uri.getScheme()) && "appassets.androidplatform.net".equals(uri.getHost());
  }
  public static boolean available(Context context) {
    try (InputStream stream = context.getAssets().open("PorticoReader.bundle/index.html")) { return stream.read() != -1; }
    catch (IOException error) { return false; }
  }
  private static WebResourceResponse denied() {
    return new WebResourceResponse("text/plain", "utf-8", 403, "Forbidden", new HashMap<>(), new ByteArrayInputStream(new byte[0]));
  }
  public static WebResourceResponse response(Context context, WebResourceRequest request) {
    Uri uri = request.getUrl(); String path = uri.getEncodedPath();
    if (!isOrigin(uri.toString()) || uri.getPort() != -1 || uri.getEncodedUserInfo() != null || uri.getEncodedQuery() != null ||
        uri.getEncodedFragment() != null || path == null || path.length() > 4096 || !path.startsWith(PREFIX) ||
        !("GET".equals(request.getMethod()) || "HEAD".equals(request.getMethod()))) return denied();
    String member = path.substring(PREFIX.length());
    if (!member.matches("[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)*")) return denied();
    int dot = member.lastIndexOf('.'); if (dot < 0) return denied();
    String extension = member.substring(dot + 1).toLowerCase(Locale.ROOT), mime;
    switch (extension) {
      case "html": mime = "text/html"; break;
      case "js": case "mjs": mime = "text/javascript"; break;
      case "css": mime = "text/css"; break;
      case "json": case "map": mime = "application/json"; break;
      case "wasm": mime = "application/wasm"; break;
      case "ttf": mime = "font/ttf"; break;
      case "otf": mime = "font/otf"; break;
      case "woff": mime = "font/woff"; break;
      case "woff2": mime = "font/woff2"; break;
      case "svg": mime = "image/svg+xml"; break;
      case "png": mime = "image/png"; break;
      case "jpg": case "jpeg": mime = "image/jpeg"; break;
      case "webp": mime = "image/webp"; break;
      case "ico": mime = "image/x-icon"; break;
      default: return denied();
    }
    try {
      InputStream asset = context.getAssets().open("PorticoReader.bundle/" + member);
      if (asset.available() > LIMIT) { asset.close(); return denied(); }
      InputStream body;
      if ("HEAD".equals(request.getMethod())) {asset.close(); body = new ByteArrayInputStream(new byte[0]);}
      else body = new FilterInputStream(asset) {
        long remaining = LIMIT;
        @Override public int read() throws IOException {
          int value = super.read(); if (value != -1 && --remaining < 0) {close(); throw new IOException("reader_asset_limit");} return value;
        }
        @Override public int read(byte[] buffer, int offset, int length) throws IOException {
          int count = in.read(buffer, offset, (int)Math.min(length, remaining + 1));
          if (count > 0 && (remaining -= count) < 0) {close(); throw new IOException("reader_asset_limit");} return count;
        }
      };
      Map<String,String> headers = new HashMap<>();
      headers.put("Cache-Control", "no-store"); headers.put("X-Content-Type-Options", "nosniff");
      headers.put("Content-Security-Policy", "default-src 'none'; script-src 'self' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; font-src 'self' blob: data:; connect-src 'self' blob:; frame-src blob:; worker-src 'self' blob:; base-uri 'none'; form-action 'none'");
      return new WebResourceResponse(mime, mime.startsWith("text/") || mime.equals("application/json") ? "utf-8" : null, 200, "OK", headers, body);
    } catch (IOException error) { return denied(); }
  }
  private PorticoReaderAndroidAssets() {}
}
