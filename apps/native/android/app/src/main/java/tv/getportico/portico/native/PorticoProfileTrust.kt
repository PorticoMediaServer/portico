package tv.getportico.portico.native

import android.util.Base64
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.IvParameterSpec
import javax.crypto.spec.SecretKeySpec
import org.bouncycastle.crypto.digests.SHA256Digest
import org.bouncycastle.crypto.generators.PKCS5S2ParametersGenerator
import org.bouncycastle.crypto.params.KeyParameter
import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener

/**
 * The profile trust vault: the port of `Native/PorticoProfileTrust.h`, rule for rule.
 *
 * Profile proofs have their own namespace, apart from account credentials, the selected server
 * session and playback journals. Callers hold the record store's lock across every operation,
 * including a failed-attempt write. No PIN and no derived key is ever stored.
 *
 * A proof is sealed with keys derived (PBKDF2-HMAC-SHA256, 200,000 rounds) from the PIN, a
 * device key and the scope; AES-256-CBC with an HMAC over every field. The device key and the
 * records are themselves files in the Keystore-sealed record store.
 */
internal class PorticoProfileTrust(private val store: PorticoRecordStore) {

  enum class Status { OK, NOT_FOUND, DECODE, AUTH_FAILED, PARAM, INTERNAL, STORAGE }

  /** The last operation's outcome, as the Apple helpers report through their `OSStatus *`. */
  var status = Status.OK
    private set

  fun read(key: String): ByteArray? = when (val found = store.read(PorticoRecordStore.PROFILE_TRUST, key)) {
    is RecordRead.Value -> { status = Status.OK; found.bytes }
    RecordRead.Missing -> { status = Status.NOT_FOUND; null }
    RecordRead.Unreadable -> { status = Status.STORAGE; null }
  }

  /** A successful call alone does not qualify a durable PIN-attempt write: the store reads it back. */
  fun write(key: String, data: ByteArray?): Status =
      if (store.write(PorticoRecordStore.PROFILE_TRUST, key, data)) Status.OK else Status.STORAGE

  fun installation(): String? {
    val data = read("installation")
    if (status == Status.OK) {
      val value = data?.toString(Charsets.UTF_8)
      if (!text(value, 128)) status = Status.DECODE
      return if (status == Status.OK) value else null
    }
    if (status != Status.NOT_FOUND) return null
    val value = UUID.randomUUID().toString()
    status = write("installation", value.toByteArray(Charsets.UTF_8))
    return if (status == Status.OK) value else null
  }

  private fun deviceKey(create: Boolean): ByteArray? {
    val data = read("device-key")
    if (status == Status.OK) {
      if (data?.size != 32) status = Status.DECODE
      return if (status == Status.OK) data else null
    }
    if (status != Status.NOT_FOUND || !create) return null
    // Never replace the key behind existing proofs. Decryption is read-only; an explicitly
    // emptied vault can generate a new key on its next online save.
    val indexData = read("index.v1")
    val checked = status
    val index = parse(indexData)
    if ((checked != Status.NOT_FOUND && checked != Status.OK) ||
        (checked == Status.OK && (index !is JSONArray || index.length() != 0))) {
      status = Status.DECODE
      return null
    }
    val bytes = ByteArray(32).also { SecureRandom().nextBytes(it) }
    status = write("device-key", bytes)
    return if (status == Status.OK) bytes else null
  }

  fun recordKey(scope: String?): String? {
    if (!text(scope, 2048)) return null
    val parts = parse(scope!!.toByteArray(Charsets.UTF_8)) as? JSONArray ?: return null
    if (parts.length() != 6 || parts.opt(0) != "portico-profile-trust-v1" || (parts.opt(1) as? String) !in listOf("hosted", "local")) return null
    for (i in 2 until 6) if (!text(parts.opt(i), 128)) return null
    return "profile:" + PorticoRecordStore.hex(MessageDigest.getInstance("SHA-256").digest(scope.toByteArray(Charsets.UTF_8)))
  }

  fun pin(pin: String?, required: Boolean): Boolean {
    if (pin == null) return false
    if (!required) return pin.isEmpty()
    return pin.length == 4 && pin.all { it in '0'..'9' }
  }

  private fun keys(scope: String, pin: String, salt: ByteArray, create: Boolean): ByteArray? {
    val device = deviceKey(create) ?: return null
    val material = pin.toByteArray(Charsets.UTF_8) + device + scope.toByteArray(Charsets.UTF_8)
    return try {
      val generator = PKCS5S2ParametersGenerator(SHA256Digest())
      generator.init(material, salt, 200000)
      (generator.generateDerivedMacParameters(512) as KeyParameter).key
    } catch (_: Exception) {
      status = Status.INTERNAL
      null
    } finally {
      material.fill(0)
    }
  }

  private fun mac(scope: String, metadata: String, salt: ByteArray, iv: ByteArray, cipher: ByteArray, key: ByteArray): ByteArray {
    val mac = Mac.getInstance("HmacSHA256")
    mac.init(SecretKeySpec(key, "HmacSHA256"))
    for (part in listOf(scope.toByteArray(Charsets.UTF_8), metadata.toByteArray(Charsets.UTF_8), salt, iv, cipher)) {
      val length = ByteArray(8)
      var n = part.size.toLong()
      for (i in 7 downTo 0) { length[i] = (n and 0xff).toByte(); n = n ushr 8 }
      mac.update(length)
      mac.update(part)
    }
    return mac.doFinal()
  }

  fun index(): List<String>? {
    val raw = read("index.v1")
    if (status == Status.NOT_FOUND) { status = Status.OK; return emptyList() }
    if (status != Status.OK) return null
    val value = parse(raw)
    if (value !is JSONArray || value.length() > 64) { status = Status.DECODE; return null }
    val keys = ArrayList<String>(value.length())
    for (i in 0 until value.length()) {
      val key = value.opt(i)
      if (!text(key, 80) || !(key as String).startsWith("profile:")) { status = Status.DECODE; return null }
      keys.add(key)
    }
    return keys
  }

  /**
   * Expired entries must not fill an invisible, unremovable 64-profile vault. Pruning only
   * removes proof material; malformed records still fail closed.
   */
  fun activeIndex(): List<String>? {
    val index = index() ?: return null
    val next = ArrayList<String>(index.size)
    val now = System.currentTimeMillis().toDouble()
    for (key in index) {
      val raw = read(key)
      if (status == Status.NOT_FOUND) { status = Status.OK; continue }
      if (status != Status.OK) return null
      val record = parse(raw)
      if (record !is JSONObject || !text(record.opt("metadata"), 8192)) { status = Status.DECODE; return null }
      val metadata = parse(record.getString("metadata").toByteArray(Charsets.UTF_8))
      val expires = number((metadata as? JSONObject)?.opt("expiresAtMs"))
      if (expires == null || expires <= 0) { status = Status.DECODE; return null }
      if (expires <= now) {
        status = write(key, null)
        if (status != Status.OK) return null
      } else {
        next.add(key)
      }
    }
    if (next.size != index.size) {
      status = write("index.v1", json(next))
      if (status != Status.OK) return null
    }
    return next
  }

  fun metadata(scope: String, metadata: Any?, installation: String): Boolean {
    val parts = parse(scope.toByteArray(Charsets.UTF_8))
    if (metadata !is JSONObject || parts !is JSONArray || parts.length() != 6) return false
    if (metadata.opt("authority") != parts.opt(1) || metadata.opt("accountId") != parts.opt(2) ||
        metadata.opt("serverId") != parts.opt(3) || metadata.opt("profileId") != parts.opt(4) ||
        metadata.opt("installationId") != parts.opt(5) || metadata.opt("installationId") != installation) return false
    if (!text(metadata.opt("profileName"), 120) || !text(metadata.opt("serverName"), 2048) ||
        !text(metadata.opt("serverUrl"), 4096) || !flagOrNumber(metadata.opt("pinRequired"))) return false
    val now = System.currentTimeMillis().toDouble()
    val expires = (metadata.opt("expiresAtMs") as? Number)?.toDouble() ?: return false
    return expires.isFinite() && expires > now && expires <= now + 30.0 * 86400000 + 60000
  }

  fun envelope(scope: String, metadataJSON: String, payloadJSON: String?, pin: String): JSONObject? {
    val plain = payloadJSON?.toByteArray(Charsets.UTF_8)
    if (plain == null || plain.size > 32768 || parse(plain) !is JSONObject) { status = Status.PARAM; return null }
    val random = SecureRandom()
    val salt = ByteArray(32).also { random.nextBytes(it) }
    val iv = ByteArray(16).also { random.nextBytes(it) }
    val keys = keys(scope, pin, salt, create = true) ?: return null
    try {
      val cipher = Cipher.getInstance("AES/CBC/PKCS5Padding")
      cipher.init(Cipher.ENCRYPT_MODE, SecretKeySpec(keys, 0, 32, "AES"), IvParameterSpec(iv))
      val sealed = cipher.doFinal(plain)
      val tag = mac(scope, metadataJSON, salt, iv, sealed, keys.copyOfRange(32, 64))
      status = Status.OK
      return JSONObject()
          .put("version", 1).put("scope", scope).put("metadata", metadataJSON)
          .put("salt", base64(salt)).put("iv", base64(iv)).put("cipher", base64(sealed)).put("mac", base64(tag))
          .put("attempts", 0).put("lockedUntil", 0)
    } catch (_: Exception) {
      status = Status.INTERNAL
      return null
    } finally {
      keys.fill(0)
    }
  }

  fun decrypt(record: JSONObject, scope: String, pin: String): String? {
    if (number(record.opt("version")) != 1.0 || record.opt("scope") != scope || !text(record.opt("metadata"), 8192)) {
      status = Status.DECODE
      return null
    }
    for (key in listOf("salt", "iv", "cipher", "mac")) if (!text(record.opt(key), 65536)) { status = Status.DECODE; return null }
    val salt = unbase64(record.getString("salt"))
    val iv = unbase64(record.getString("iv"))
    val sealed = unbase64(record.getString("cipher"))
    val tag = unbase64(record.getString("mac"))
    if (salt?.size != 32 || iv?.size != 16 || sealed == null || sealed.size < 16 || sealed.size > 32784 || tag?.size != 32) {
      status = Status.DECODE
      return null
    }
    val keys = keys(scope, pin, salt, create = false) ?: return null
    try {
      val expected = mac(scope, record.getString("metadata"), salt, iv, sealed, keys.copyOfRange(32, 64))
      if (!MessageDigest.isEqual(tag, expected)) { status = Status.AUTH_FAILED; return null }
      val plain = try {
        val cipher = Cipher.getInstance("AES/CBC/PKCS5Padding")
        cipher.init(Cipher.DECRYPT_MODE, SecretKeySpec(keys, 0, 32, "AES"), IvParameterSpec(iv))
        cipher.doFinal(sealed)
      } catch (_: Exception) {
        status = Status.DECODE
        return null
      }
      val payload = plain.toString(Charsets.UTF_8)
      val valid = parse(plain) is JSONObject
      plain.fill(0)
      status = if (valid) Status.OK else Status.DECODE
      return if (valid) payload else null
    } finally {
      keys.fill(0)
    }
  }

  companion object {
    fun json(value: List<*>): ByteArray = JSONArray(value).toString().toByteArray(Charsets.UTF_8)

    fun json(value: JSONObject): ByteArray = value.toString().toByteArray(Charsets.UTF_8)

    /** The parsed JSON value, or null when the bytes are not one. */
    fun parse(bytes: ByteArray?): Any? = try {
      bytes?.let { JSONTokener(it.toString(Charsets.UTF_8)).nextValue() }
    } catch (_: Exception) {
      null
    }

    /** A non-empty string within [max] characters, with no control or format characters. */
    fun text(value: Any?, max: Int): Boolean {
      if (value !is String || value.isEmpty() || value.length > max) return false
      return value.none { Character.getType(it).let { type -> type == Character.CONTROL.toInt() || type == Character.FORMAT.toInt() } }
    }

    /** A finite JSON number. A boolean is not a number here. */
    fun number(value: Any?): Double? = (value as? Number)?.toDouble()?.takeIf { it.isFinite() }

    private fun flagOrNumber(value: Any?): Boolean = value is Boolean || value is Number

    /** `pinRequired` as the Apple vault reads it: a boolean, or a number that is not zero. */
    fun flag(value: Any?): Boolean = when (value) {
      is Boolean -> value
      is Number -> value.toDouble() != 0.0
      else -> false
    }

    /**
     * Corrupt or fractional counters must fail closed, not be clamped back to zero and silently
     * reset the durable guessing delay.
     */
    fun attempts(record: JSONObject): Boolean {
      val count = number(record.opt("attempts")) ?: return false
      val until = number(record.opt("lockedUntil")) ?: return false
      return count >= 0 && count <= 1000 && Math.floor(count) == count && until >= 0
    }

    private fun base64(bytes: ByteArray): String = Base64.encodeToString(bytes, Base64.NO_WRAP)

    private fun unbase64(text: String): ByteArray? = try {
      Base64.decode(text, Base64.NO_WRAP)
    } catch (_: IllegalArgumentException) {
      null
    }
  }
}
