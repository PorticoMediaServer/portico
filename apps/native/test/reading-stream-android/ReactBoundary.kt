package com.facebook.react.bridge

annotation class ReactMethod
interface Promise {fun resolve(value: Any?); fun reject(code: String, message: String?)}
enum class ReadableType {String, Null}
interface ReadableMapKeySetIterator {fun hasNextKey(): Boolean; fun nextKey(): String}
interface ReadableMap {fun keySetIterator(): ReadableMapKeySetIterator; fun getType(key: String): ReadableType; fun getString(key: String): String?}
class HeaderMap(private val values: Map<String, String>): ReadableMap {
  override fun getType(key: String) = if (values.containsKey(key)) ReadableType.String else ReadableType.Null
  override fun getString(key: String) = values[key]
  override fun keySetIterator(): ReadableMapKeySetIterator {
    val iterator = values.keys.iterator()
    return object: ReadableMapKeySetIterator {override fun hasNextKey() = iterator.hasNext(); override fun nextKey() = iterator.next()}
  }
}
class BridgeMap {
  val values = mutableMapOf<String, Any?>()
  fun putInt(key: String, value: Int) {values[key] = value}
  fun putBoolean(key: String, value: Boolean) {values[key] = value}
  fun putString(key: String, value: String?) {values[key] = value}
  fun putMap(key: String, value: BridgeMap) {values[key] = value}
}
object Arguments {fun createMap() = BridgeMap()}
