package tv.getportico.portico.native

import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.ContentProvider
import android.content.ContentUris
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.database.Cursor
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.tv.TvContract
import android.net.Uri
import android.os.Build
import android.os.ParcelFileDescriptor
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.getportico.portico.R

/**
 * `NativeModules.PorticoTopShelf`: the television's home screen. The Android counterpart of the
 * tvOS Top Shelf: the same four methods, the same small feed (`/v1/topshelf`) read with the same
 * token, drawn as the system's **Watch Next** row (Continue Watching) and one Portico channel (the
 * next row of the viewer's Home). Televisions only.
 *
 * The app gives this the server's address and a token that reads that one feed and nothing else.
 * The token is sealed in the record store; a shelf held for another viewer is replaced, never
 * reused.
 */
class PorticoTopShelf(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  override fun getName(): String = "PorticoTopShelf"

  @ReactMethod
  fun configure(baseUrl: String?, token: String?, expiresAt: String?, owner: String?, promise: Promise) {
    if (baseUrl == null || token == null || expiresAt == null || owner == null || token.length > 8192 || owner.length > 512) {
      promise.reject("topshelf_invalid", "The shelf configuration is invalid.")
      return
    }
    val context = reactApplicationContext.applicationContext
    PorticoHomeShelf.work.execute {
      PorticoHomeShelf.configure(context, baseUrl, token, expiresAt, owner)
      promise.resolve(null)
    }
  }

  @ReactMethod
  fun expiresAt(promise: Promise) {
    val context = reactApplicationContext.applicationContext
    PorticoHomeShelf.work.execute {
      val held = PorticoHomeShelf.held(context)
      if (held == null) promise.resolve(null) else promise.resolve(Arguments.makeNativeMap(held))
    }
  }

  @ReactMethod
  fun clear(promise: Promise) {
    val context = reactApplicationContext.applicationContext
    PorticoHomeShelf.work.execute { PorticoHomeShelf.clear(context); promise.resolve(null) }
  }

  /** Called after something was watched, so the home screen's Continue Watching is not a day old. */
  @ReactMethod
  fun changed() {
    val context = reactApplicationContext.applicationContext
    PorticoHomeShelf.work.execute { PorticoHomeShelf.refresh(context) }
  }
}

internal object PorticoHomeShelf {
  val work = Executors.newSingleThreadExecutor { Thread(it, "portico-home-shelf") }
  private const val PREFERENCES = "portico.topshelf"
  private const val SERVICE = "topshelf"
  private const val ACCOUNT = "feed"
  private const val MAX_CONTINUE = 10
  private const val MAX_NEXT = 10
  private const val JOB = 4102
  const val AUTHORITY = "tv.getportico.portico.shelf"
  private val http: OkHttpClient by lazy {
    OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null).callTimeout(15, TimeUnit.SECONDS).build()
  }

  private fun preferences(context: Context) = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
  private fun token(context: Context): String? = (PorticoRecordStore.get(context).read(SERVICE, ACCOUNT) as? RecordRead.Value)?.bytes?.toString(Charsets.UTF_8)?.takeIf { it.isNotEmpty() }
  fun artDirectory(context: Context) = File(context.cacheDir, "topshelf-art")

  fun configure(context: Context, baseUrl: String, token: String, expiresAt: String, owner: String) {
    val preferences = preferences(context)
    if (preferences.getString("owner", null) != owner) { artDirectory(context).deleteRecursively(); removeRows(context) }
    PorticoRecordStore.get(context).write(SERVICE, ACCOUNT, token.toByteArray(Charsets.UTF_8))
    preferences.edit().putString("baseUrl", baseUrl).putString("expiresAt", expiresAt).putString("owner", owner).apply()
    schedule(context)
    refresh(context)
  }

  fun held(context: Context): Map<String, Any?>? {
    // Without a token the home screen can't draw anything: report nothing held and let the app renew.
    if (token(context) == null) return null
    val preferences = preferences(context)
    val base = preferences.getString("baseUrl", null) ?: return null
    val expires = preferences.getString("expiresAt", null) ?: return null
    return mapOf("baseUrl" to base, "expiresAt" to expires, "owner" to (preferences.getString("owner", "") ?: ""))
  }

  fun clear(context: Context) {
    preferences(context).edit().clear().apply()
    PorticoRecordStore.get(context).write(SERVICE, ACCOUNT, null)
    artDirectory(context).deleteRecursively()
    removeRows(context)
    try { (context.getSystemService(Context.JOB_SCHEDULER_SERVICE) as JobScheduler).cancel(JOB) } catch (_: Exception) {}
  }

  private fun schedule(context: Context) {
    try {
      val job = JobInfo.Builder(JOB, ComponentName(context, PorticoShelfJob::class.java))
          .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY).setPeriodic(TimeUnit.HOURS.toMillis(6)).setPersisted(false).build()
      (context.getSystemService(Context.JOB_SCHEDULER_SERVICE) as JobScheduler).schedule(job)
    } catch (_: Exception) {}
  }

  /** Reads the feed and redraws the home screen's rows. Runs on [work] or a job's own thread. */
  fun refresh(context: Context) {
    if (Build.VERSION.SDK_INT < 26) return
    val token = token(context) ?: return
    val base = preferences(context).getString("baseUrl", null) ?: return
    val origin = try { Uri.parse(base) } catch (_: Exception) { return }
    if (origin.host.isNullOrEmpty() || (origin.scheme != "https" && origin.scheme != "http")) return
    val feed: Map<*, *>
    try {
      http.newCall(Request.Builder().url("$base/v1/topshelf").header("Authorization", "Bearer $token").header("Accept", "application/json").build()).execute().use { response ->
        // An expired or revoked token must not keep showing someone's history.
        if (response.code == 401 || response.code == 403) { artDirectory(context).deleteRecursively(); removeRows(context); return }
        if (response.code != 200) return
        val body = response.body?.source()?.let { source -> source.request(2L shl 20); if (source.buffer.size > (1L shl 20)) return; source.buffer.readByteArray() } ?: return
        feed = PorticoListeningOwner.parse(body) as? Map<*, *> ?: return
      }
    } catch (_: Exception) {
      return // the home screen keeps whatever it had
    }
    val sections = (feed["sections"] as? List<*>)?.filterIsInstance<Map<*, *>>() ?: return
    val continuing = entries(sections.firstOrNull { it["id"] == "continue" }).take(MAX_CONTINUE)
    val seen = continuing.map { it["id"] }.toSet()
    var nextTitle = ""
    var nextSquare = false
    var next: List<Map<*, *>> = emptyList()
    for (section in sections) {
      if (section["id"] == "continue") continue
      val fresh = entries(section).filter { it["id"] !in seen }.take(MAX_NEXT)
      if (fresh.isNotEmpty()) { next = fresh; nextTitle = section["title"] as? String ?: ""; nextSquare = section["shape"] == "square"; break }
    }
    for (entry in continuing + next) poster(context, origin, token, entry)
    try { draw(context, continuing, next, nextTitle, nextSquare) } catch (_: Exception) {}
  }

  private fun entries(section: Map<*, *>?): List<Map<*, *>> =
      (section?.get("entries") as? List<*>)?.filterIsInstance<Map<*, *>>()?.filter { it["id"] is String && it["title"] is String && (it["displayUrl"] as? String)?.startsWith("portico://") == true } ?: emptyList()

  private fun artName(id: String): String = PorticoRecordStore.hex(MessageDigest.getInstance("SHA-256").digest(id.toByteArray(Charsets.UTF_8))).take(32) + ".jpg"

  /** The poster, fetched once with the feed's token and kept small. The server's address is used as given: its query carries the artwork grant. */
  private fun poster(context: Context, origin: Uri, token: String, entry: Map<*, *>) {
    val path = entry["imageUrl"] as? String ?: return
    val file = File(artDirectory(context), artName(entry["id"] as String))
    if (file.isFile) return
    val url = try { if (path.startsWith("/")) Uri.parse(origin.toString() + path) else Uri.parse(path) } catch (_: Exception) { return }
    if (url.host?.lowercase() != origin.host?.lowercase() || url.port != origin.port || url.scheme != origin.scheme) return
    try {
      http.newCall(Request.Builder().url(url.toString()).header("Authorization", "Bearer $token").build()).execute().use { response ->
        if (response.code != 200) return
        val source = response.body?.source() ?: return
        source.request(8L shl 20)
        if (source.buffer.size >= (8L shl 20)) return
        val bytes = source.buffer.readByteArray()
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        var sample = 1
        while (bounds.outHeight / (sample * 2) >= 600) sample *= 2
        val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample }) ?: return
        artDirectory(context).mkdirs()
        val holding = File(artDirectory(context), file.name + ".tmp")
        FileOutputStream(holding).use { bitmap.compress(Bitmap.CompressFormat.JPEG, 85, it) }
        bitmap.recycle()
        holding.renameTo(file)
      }
    } catch (_: Exception) {} catch (_: OutOfMemoryError) {}
  }

  private fun intentUri(context: Context, address: String): String =
      Intent(Intent.ACTION_VIEW, Uri.parse(address)).setPackage(context.packageName).toUri(Intent.URI_INTENT_SCHEME)

  private fun artUri(context: Context, entry: Map<*, *>): Uri? {
    val name = artName(entry["id"] as String)
    return if (File(artDirectory(context), name).isFile) Uri.parse("content://$AUTHORITY/art/$name") else null
  }

  private fun removeRows(context: Context) {
    if (Build.VERSION.SDK_INT < 26) return
    try {
      context.contentResolver.delete(TvContract.WatchNextPrograms.CONTENT_URI, null, null)
      channel(context, false)?.let { context.contentResolver.delete(TvContract.buildPreviewProgramsUriForChannel(it), null, null) }
    } catch (_: Exception) {}
  }

  /** The app's one channel on the home screen. */
  private fun channel(context: Context, create: Boolean): Long? {
    val preferences = preferences(context)
    val known = preferences.getLong("channel", -1)
    if (known >= 0) {
      context.contentResolver.query(TvContract.buildChannelUri(known), arrayOf(TvContract.Channels._ID), null, null, null)?.use { if (it.moveToFirst()) return known }
    }
    if (!create) return null
    val values = ContentValues()
    values.put(TvContract.Channels.COLUMN_TYPE, TvContract.Channels.TYPE_PREVIEW)
    values.put(TvContract.Channels.COLUMN_DISPLAY_NAME, context.getString(R.string.app_name))
    (context.packageManager.getLeanbackLaunchIntentForPackage(context.packageName) ?: context.packageManager.getLaunchIntentForPackage(context.packageName))
        ?.let { values.put(TvContract.Channels.COLUMN_APP_LINK_INTENT_URI, it.toUri(Intent.URI_INTENT_SCHEME)) }
    val created = context.contentResolver.insert(TvContract.Channels.CONTENT_URI, values) ?: return null
    val id = ContentUris.parseId(created)
    preferences.edit().putLong("channel", id).apply()
    try {
      BitmapFactory.decodeResource(context.resources, R.mipmap.ic_launcher)?.let { logo ->
        context.contentResolver.openOutputStream(TvContract.buildChannelLogoUri(id))?.use { logo.compress(Bitmap.CompressFormat.PNG, 100, it) }
      }
    } catch (_: Exception) {}
    // An app's first channel is shown without asking.
    try { TvContract.requestChannelBrowsable(context, id) } catch (_: Exception) {}
    return id
  }

  private fun draw(context: Context, continuing: List<Map<*, *>>, next: List<Map<*, *>>, nextTitle: String, nextSquare: Boolean) {
    val resolver = context.contentResolver
    val now = System.currentTimeMillis()
    // Watch Next: Continue Watching, most recent first.
    resolver.delete(TvContract.WatchNextPrograms.CONTENT_URI, null, null)
    continuing.forEachIndexed { index, entry ->
      val values = ContentValues()
      values.put(TvContract.WatchNextPrograms.COLUMN_TYPE, TvContract.WatchNextPrograms.TYPE_MOVIE)
      values.put(TvContract.WatchNextPrograms.COLUMN_WATCH_NEXT_TYPE, TvContract.WatchNextPrograms.WATCH_NEXT_TYPE_CONTINUE)
      values.put(TvContract.WatchNextPrograms.COLUMN_LAST_ENGAGEMENT_TIME_UTC_MILLIS, now - index * 1000L)
      values.put(TvContract.WatchNextPrograms.COLUMN_TITLE, entry["title"] as String)
      values.put(TvContract.WatchNextPrograms.COLUMN_INTERNAL_PROVIDER_ID, entry["id"] as String)
      values.put(TvContract.WatchNextPrograms.COLUMN_INTENT_URI, intentUri(context, (entry["playUrl"] as? String)?.takeIf { it.startsWith("portico://") } ?: entry["displayUrl"] as String))
      values.put(TvContract.WatchNextPrograms.COLUMN_POSTER_ART_ASPECT_RATIO, TvContract.WatchNextPrograms.ASPECT_RATIO_2_3)
      artUri(context, entry)?.let { values.put(TvContract.WatchNextPrograms.COLUMN_POSTER_ART_URI, it.toString()) }
      // The feed gives progress as a percentage: a nominal length carries it to the progress bar.
      val percent = (PorticoPlaybackController.number(entry["progressPercent"]) ?: 0.0).coerceIn(0.0, 100.0)
      values.put(TvContract.WatchNextPrograms.COLUMN_DURATION_MILLIS, 100_000)
      values.put(TvContract.WatchNextPrograms.COLUMN_LAST_PLAYBACK_POSITION_MILLIS, (percent * 1000).toInt())
      resolver.insert(TvContract.WatchNextPrograms.CONTENT_URI, values)
    }
    // The channel: the next row of the viewer's Home. An empty row is left out.
    val id = channel(context, next.isNotEmpty()) ?: return
    resolver.delete(TvContract.buildPreviewProgramsUriForChannel(id), null, null)
    if (nextTitle.isNotEmpty()) resolver.update(TvContract.buildChannelUri(id), ContentValues().apply { put(TvContract.Channels.COLUMN_DISPLAY_NAME, nextTitle) }, null, null)
    next.forEachIndexed { index, entry ->
      val values = ContentValues()
      values.put(TvContract.PreviewPrograms.COLUMN_CHANNEL_ID, id)
      values.put(TvContract.PreviewPrograms.COLUMN_TYPE, TvContract.PreviewPrograms.TYPE_MOVIE)
      values.put(TvContract.PreviewPrograms.COLUMN_TITLE, entry["title"] as String)
      values.put(TvContract.PreviewPrograms.COLUMN_INTERNAL_PROVIDER_ID, entry["id"] as String)
      values.put(TvContract.PreviewPrograms.COLUMN_INTENT_URI, intentUri(context, entry["displayUrl"] as String))
      // A row never mixes shapes: music and audiobooks are square, film and television are posters.
      values.put(TvContract.PreviewPrograms.COLUMN_POSTER_ART_ASPECT_RATIO, if (nextSquare) TvContract.PreviewPrograms.ASPECT_RATIO_1_1 else TvContract.PreviewPrograms.ASPECT_RATIO_2_3)
      values.put(TvContract.PreviewPrograms.COLUMN_WEIGHT, next.size - index)
      artUri(context, entry)?.let { values.put(TvContract.PreviewPrograms.COLUMN_POSTER_ART_URI, it.toString()) }
      resolver.insert(TvContract.PreviewPrograms.CONTENT_URI, values)
    }
  }
}

/** Every six hours with a network: the home screen's rows are read again, so they are not a day old on a television that stayed off the app. */
class PorticoShelfJob : JobService() {
  override fun onStartJob(params: JobParameters?): Boolean {
    val context = applicationContext
    PorticoHomeShelf.work.execute {
      try { PorticoHomeShelf.refresh(context) } finally { jobFinished(params, false) }
    }
    return true
  }

  override fun onStopJob(params: JobParameters?): Boolean = false
}

/**
 * Serves the home screen's posters to the system launcher, which cannot send the app's token.
 * Read-only, and only the small pictures this app cached for its own rows.
 */
class PorticoShelfArtProvider : ContentProvider() {
  override fun onCreate(): Boolean = true

  override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor? {
    val context = context ?: return null
    val segments = uri.pathSegments
    if (mode != "r" || segments.size != 2 || segments[0] != "art" || !NAME.matches(segments[1])) throw java.io.FileNotFoundException()
    val file = File(PorticoHomeShelf.artDirectory(context), segments[1])
    if (!file.isFile) throw java.io.FileNotFoundException()
    return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
  }

  override fun getType(uri: Uri): String = "image/jpeg"
  override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
  override fun insert(uri: Uri, values: ContentValues?): Uri? = null
  override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
  override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

  private companion object { val NAME = Regex("^[0-9a-f]{32}\\.jpg$") }
}
