package tv.getportico.portico.native

import android.content.Context
import android.util.AtomicFile
import java.io.File
import java.io.FileNotFoundException

/** A bounded diagnostic file, outside Android backups and inside this app's private sandbox. */
internal class PorticoAppLog(context: Context) {
  private val file = AtomicFile(File(context.noBackupFilesDir, "app.log"))
  fun read(): String? {
    return try { file.openRead().use {
      if (it.channel.size() > MAX_BYTES) { file.delete(); null }
      else String(it.readBytes(), Charsets.UTF_8)
    } } catch (_: FileNotFoundException) { null }
  }
  fun write(text: String?) {
    if (text == null || text.isEmpty()) { file.delete(); return }
    val bytes = text.toByteArray(Charsets.UTF_8)
    require(bytes.size <= MAX_BYTES)
    val out = file.startWrite()
    try { out.write(bytes); file.finishWrite(out) }
    catch (e: Exception) { file.failWrite(out); throw e }
  }
  companion object { private const val MAX_BYTES = 1024 * 1024 }
}
