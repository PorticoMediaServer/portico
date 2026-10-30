package tv.getportico.portico.native

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.ImageFormat
import android.hardware.Camera
import android.os.Build
import android.os.Bundle
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import java.lang.ref.WeakReference
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors

/** Camera preview and QR-only decoding stay on device. No image/file/network output. */
@Suppress("DEPRECATION")
class PorticoQRScanActivity : Activity(), SurfaceHolder.Callback {
  private lateinit var requestId: String
  private lateinit var preview: SurfaceView
  private val decoder = Executors.newSingleThreadExecutor { Thread(it, "portico-qr") }
  private var camera: Camera? = null
  private var surfaceReady = false
  private var resumed = false
  @Volatile private var ended = false
  private var backCallback: Any? = null

  override fun onCreate(state: Bundle?) {
    super.onCreate(state)
    requestId = intent.getStringExtra("requestId") ?: ""
    if (!requestId.matches(Regex("[a-f0-9]{32}"))) { finish(); return }
    if (!isAdmitted(requestId)) { complete(null); return }
    instances[requestId] = WeakReference(this)
    val column = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(android.graphics.Color.BLACK) }
    column.addView(TextView(this).apply { text = intent.getStringExtra("title"); textSize = 22f; setTextColor(android.graphics.Color.WHITE); setPadding(24, 24, 24, 24) })
    preview = SurfaceView(this); preview.holder.addCallback(this)
    column.addView(preview, LinearLayout.LayoutParams(-1, 0, 1f))
    column.addView(Button(this).apply { text = intent.getStringExtra("cancel"); setOnClickListener { complete(null) } })
    setContentView(column)
    if (Build.VERSION.SDK_INT >= 33) {
      backCallback = BackApi33.register(this)
    }
    if (!isAdmitted(requestId)) complete(null)
    else if (!packageManager.hasSystemFeature(PackageManager.FEATURE_CAMERA_ANY)) complete(null, "qr_camera_unavailable")
    else if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
      if (isAdmitted(requestId)) requestPermissions(arrayOf(Manifest.permission.CAMERA), 1) else complete(null)
    }
  }
  override fun onResume() { super.onResume(); resumed = true; openCamera() }
  override fun onPause() { resumed = false; closeCamera(); super.onPause() }
  override fun onStop() { if (!isFinishing && !isChangingConfigurations) complete(null); super.onStop() }
  override fun onBackPressed() { complete(null) }
  override fun onRequestPermissionsResult(code: Int, permissions: Array<out String>, results: IntArray) {
    super.onRequestPermissionsResult(code, permissions, results)
    if (code == 1 && !ended) {
      if (results.firstOrNull() == PackageManager.PERMISSION_GRANTED) openCamera() else complete(null, "qr_permission_denied")
    }
  }
  override fun surfaceCreated(holder: SurfaceHolder) { surfaceReady = true; openCamera() }
  override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {}
  override fun surfaceDestroyed(holder: SurfaceHolder) { surfaceReady = false; closeCamera() }
  private fun openCamera() {
    if (ended || !isAdmitted(requestId) || !resumed || !surfaceReady || camera != null || checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) return
    try {
      val count = Camera.getNumberOfCameras()
      if (count == 0) { complete(null, "qr_camera_unavailable"); return }
      val id = (0 until count).firstOrNull { Camera.CameraInfo().also { info -> Camera.getCameraInfo(it, info) }.facing == Camera.CameraInfo.CAMERA_FACING_BACK } ?: 0
      if (!isAdmitted(requestId)) { complete(null); return }
      val opened = Camera.open(id); camera = opened
      val parameters = opened.parameters
      val size = parameters.supportedPreviewSizes.filter { it.width * it.height <= 1280 * 720 }.maxByOrNull { it.width * it.height }
        ?: throw IllegalStateException("camera_preview_unavailable")
      if (!parameters.supportedPreviewFormats.contains(ImageFormat.NV21)) throw IllegalStateException("camera_format_unavailable")
      parameters.setPreviewSize(size.width, size.height); parameters.previewFormat = ImageFormat.NV21
      if (parameters.supportedFocusModes?.contains(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE) == true) parameters.focusMode = Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE
      opened.parameters = parameters
      val info = Camera.CameraInfo(); Camera.getCameraInfo(id, info)
      val rotation = when (windowManager.defaultDisplay.rotation) { Surface.ROTATION_90 -> 90; Surface.ROTATION_180 -> 180; Surface.ROTATION_270 -> 270; else -> 0 }
      val orientation = if (info.facing == Camera.CameraInfo.CAMERA_FACING_FRONT) (360 - (info.orientation + rotation) % 360) % 360 else (info.orientation - rotation + 360) % 360
      opened.setDisplayOrientation(orientation); opened.setPreviewDisplay(preview.holder)
      if (!isAdmitted(requestId)) { complete(null); return }
      opened.startPreview(); nextFrame(opened, size.width, size.height)
    } catch (_: Exception) { complete(null, "qr_camera_unavailable") }
  }
  private fun nextFrame(owned: Camera, width: Int, height: Int) {
    if (ended || !isAdmitted(requestId) || camera !== owned || !resumed) return
    try { owned.setOneShotPreviewCallback { bytes, _ ->
      if (ended || !isAdmitted(requestId) || camera !== owned || !resumed || bytes == null) return@setOneShotPreviewCallback
      val frame = bytes.copyOf()
      decoder.execute {
        var raw: String? = null
        if (!ended) try {
          val luminance = PlanarYUVLuminanceSource(frame, width, height, 0, 0, width, height, false)
          raw = QRCodeReader().decode(BinaryBitmap(HybridBinarizer(luminance)), mapOf(DecodeHintType.TRY_HARDER to true)).text
        } catch (_: Exception) { /* Not a QR frame; exactly one next frame is requested below. */ }
        runOnUiThread {
          if (ended || !isAdmitted(requestId) || camera !== owned || !resumed) return@runOnUiThread
          val answer = raw
          if (answer != null) {
            if (answer.toByteArray(Charsets.UTF_8).size !in 1..8192) complete(null, "qr_too_large") else complete(answer)
          } else nextFrame(owned, width, height)
        }
      }
    } } catch (_: Exception) { complete(null, "qr_camera_unavailable") }
  }
  private fun closeCamera() { val owned = camera; camera = null; try { owned?.setPreviewCallback(null); owned?.stopPreview() } catch (_: Exception) {} finally { try { owned?.release() } catch (_: Exception) {} } }
  private fun complete(raw: String?, failure: String? = null) {
    if (ended) return
    val admitted = isAdmitted(requestId)
    val answer = if (admitted) raw else null
    val reason = if (admitted) failure else null
    ended = true; release(requestId); closeCamera()
    setResult(if (answer != null) RESULT_OK else RESULT_CANCELED, Intent().putExtra("requestId", requestId).putExtra("raw", answer).putExtra("failure", reason))
    finish()
  }
  override fun onDestroy() {
    ended = true; release(requestId); closeCamera(); decoder.shutdownNow(); instances.remove(requestId)
    if (Build.VERSION.SDK_INT >= 33) backCallback?.let { BackApi33.unregister(this, it) }
    super.onDestroy()
  }
  private object BackApi33 {
    fun register(activity: PorticoQRScanActivity): Any {
      val callback = android.window.OnBackInvokedCallback { activity.complete(null) }
      activity.onBackInvokedDispatcher.registerOnBackInvokedCallback(android.window.OnBackInvokedDispatcher.PRIORITY_DEFAULT, callback)
      return callback
    }
    fun unregister(activity: PorticoQRScanActivity, callback: Any) { activity.onBackInvokedDispatcher.unregisterOnBackInvokedCallback(callback as android.window.OnBackInvokedCallback) }
  }
  companion object {
    @Volatile private var admittedId: String? = null
    @Synchronized fun admit(id: String): Boolean { if (admittedId != null) return false; admittedId = id; return true }
    private fun isAdmitted(id: String) = admittedId == id
    @Synchronized private fun release(id: String) { if (admittedId == id) admittedId = null }
    private val instances = ConcurrentHashMap<String, WeakReference<PorticoQRScanActivity>>()
    fun retireAdmission(id: String) { release(id) }
    fun cancel(id: String) { release(id); instances[id]?.get()?.let { activity -> activity.runOnUiThread { activity.complete(null) } } }
  }
}
