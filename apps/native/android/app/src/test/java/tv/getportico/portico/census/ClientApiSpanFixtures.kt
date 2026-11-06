@file:Suppress("unused", "UNUSED_PARAMETER", "UNUSED_VARIABLE")

package tv.getportico.portico.census

import java.io.IOException

// Compiled by native test compilation and read by the ownership-model tests.
// Private stand-ins keep the grammar probes independent of networking or Android.
private class HttpUrl
private class Request {
  class Builder {
    fun url(value: HttpUrl): Builder = this
    fun build(): Request = Request()
  }
  fun newBuilder(): Builder = Builder()
}
@Target(AnnotationTarget.FUNCTION, AnnotationTarget.TYPE)
private annotation class Étiquette

// CASE anonymous-nullable
private object AnonymousNullable {
  private infix fun Request?.inline(fn: Request?.() -> Request?): Request? = fn()
  private fun relay(url: HttpUrl, other: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ inline fun Request?.(): Request? = this?.newBuilder()?.url(other)?.build()
}

// CASE anonymous-parenthesized
private object AnonymousParenthesized {
  private infix fun Request?.inline(fn: Request?.() -> Request?): Request? = fn()
  private fun relay(url: HttpUrl, other: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /* outer
    /* nested */
  */ inline fun (Request?).(): Request? = this?.newBuilder()?.url(other)?.build()
}

// CASE anonymous-generic
private object AnonymousGeneric {
  private infix fun Request?.inline(fn: List<Request?>.() -> Request?): Request? = fn(emptyList())
  private fun relay(url: HttpUrl, other: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ inline fun List<Request?>.(): Request? = firstOrNull()?.newBuilder()?.url(other)?.build()
}

// CASE unicode-infix
private object UnicodeInfix {
  private infix fun Request?.élève(fn: Request?.() -> Request?): Request? = fn()
  private fun relay(url: HttpUrl, other: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ élève fun Request?.(): Request? = this?.newBuilder()?.url(other)?.build()
}

// CASE postfix
private object Postfix {
  private fun relay(url: HttpUrl, other: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ ?.newBuilder()?.url(other)?.build()
}

// CASE elvis
private object Elvis {
  private fun relay(url: HttpUrl, other: HttpUrl): Request = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ ?: Request.Builder().url(other).build()
}

// CASE operator
private object Operator {
  private operator fun Request?.plus(other: HttpUrl): Request? = this?.newBuilder()?.url(other)?.build()
  private fun relay(url: HttpUrl, other: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ + other
}

// CASE cast
private object Cast {
  private fun relay(url: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null } /*
  */ as Request?
}

// CASE unicode-function
private object UnicodeFunction {
  private fun relay(url: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null }
  internal fun élève() = 1
  internal fun bulkMetadataPreview() = 1
}

// CASE unicode-annotation
private object UnicodeAnnotation {
  private fun relay(url: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null }
  @Étiquette
  internal fun unrelated() = 1
  internal fun bulkMetadataPreview() = 1
}

// CASE named-receivers
private object NamedReceivers {
  private fun relay(url: HttpUrl): Request? = try {
    Request.Builder().url(url).build()
  } catch (e: IOException) { null }
  private fun Request?.named() = this
  private fun tv.getportico.portico.census.Request?.qualified() = this
  private fun (Request?).parenthesized() = this
  private fun <T> List<T>.generic() = size
  private fun ((Request?) -> Request?).functionReceiver() = this(null)
  private fun @Étiquette Request?.annotated() = this
  private fun Request?.`escaped name`() = this
  private fun Request?.`fun`() = this
  internal fun Σ() = 1
  internal fun 漢字() = 1
  internal fun ʰ() = 1
  internal fun ǅ() = 1
  internal fun élève١() = 1
}

// CASE literal-trivia
private object LiteralTrivia {
  private fun relay(url: HttpUrl): Request? = try {
    val quoted = "${if (true) "} catch (fake: Exception) {" else "fun fake() ="}"
    val raw = """raw } catch (fake: Exception) {
      ${"""nested raw } fun fake() {"""}
      /* not a comment */
    """
    val escaped = "quote: \" and slash: \\ and dollar: \$"
    val rawQuotes = """end""""
    val rawFive = """end"""""
    val `fun fake } (` = 1
    val character = '}'
    /* outer comment: }
      /* nested } catch (fake: Exception) { */
    */
    Request.Builder().url(url).build()
  } catch (e: IOException) { null }
  @Étiquette
  internal fun élève() = "unchanged sibling"
}
