package tv.getportico.portico.native

import android.system.Os
import android.system.OsConstants
import java.io.File
import java.util.ArrayDeque

/** Logical file bytes and actual allocated blocks are distinct from READY artifact quota bytes. */
internal data class DownloadStorageUsage(val storedBytes: Long, val allocatedBytes: Long)
internal data class DownloadStorageNode(val device: Long, val inode: Long, val directory: Boolean, val bytes: Long, val allocatedBytes: Long)
internal object PorticoDownloadStorage {
  private fun stat(file: File): DownloadStorageNode {
    // lstat never traverses a symbolic link outside the app-owned Downloads tree.
    val value = Os.lstat(file.absolutePath)
    return DownloadStorageNode(value.st_dev, value.st_ino, OsConstants.S_ISDIR(value.st_mode), if (OsConstants.S_ISREG(value.st_mode)) value.st_size else 0L, Math.multiplyExact(value.st_blocks, 512L))
  }

  /** A failed/overflowed/transiently disappearing entry is unknown, never an invented zero. */
  fun measure(
      root: File,
      read: (File) -> DownloadStorageNode = ::stat,
      children: (File) -> List<File>? = { it.listFiles()?.toList() },
  ): DownloadStorageUsage? = try {
    val pending = ArrayDeque<File>(); pending.add(root)
    val seen = HashSet<Pair<Long, Long>>()
    var stored = 0L; var allocated = 0L
    while (pending.isNotEmpty()) {
      val file = pending.removeFirst(); val value = read(file)
      require(value.bytes >= 0 && value.allocatedBytes >= 0)
      if (!seen.add(value.device to value.inode)) continue
      allocated = Math.addExact(allocated, value.allocatedBytes)
      if (value.directory) pending.addAll(children(file) ?: error("download_storage_unavailable"))
      else stored = Math.addExact(stored, value.bytes)
    }
    DownloadStorageUsage(stored, allocated)
  } catch (_: Exception) { null }
}
