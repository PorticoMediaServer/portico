package tv.getportico.portico.native

import java.io.IOException
import java.net.InetAddress
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.util.Base64
import java.util.concurrent.TimeUnit
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test

/** Only persistence is isolated. Every trust decision and TLS handshake uses production code. */
class PorticoServerTrustTest {
  // Seed an already verified identity pin without making Android Keystore part of these JVM tests.
  // Reflection stays in this fixture; production exposes no trust override or test bypass.
  @Suppress("UNCHECKED_CAST")
  private fun <T> state(name: String): T = PorticoServerTrust::class.java.getDeclaredField(name).let {
    it.isAccessible = true
    it.get(PorticoServerTrust) as T
  }
  private val pins: MutableMap<String, PorticoServerTrust.Pin> get() = state("pins")
  private val refused: MutableMap<String, String> get() = state("refused")
  private lateinit var previousPins: Map<String, PorticoServerTrust.Pin>
  private lateinit var previousRefused: Map<String, String>
  private var previousAccount: String? = null
  private val serverId = "srv_" + "A".repeat(43)

  private fun account(value: String?) {
    PorticoServerTrust::class.java.getDeclaredField("accountKey").let {
      it.isAccessible = true
      it.set(PorticoServerTrust, value)
    }
  }

  @Before fun isolatePins() {
    previousPins = pins.toMap()
    previousRefused = refused.toMap()
    previousAccount = state("accountKey")
    pins.clear()
    refused.clear()
  }

  @After fun restorePins() {
    pins.clear(); pins.putAll(previousPins)
    refused.clear(); refused.putAll(previousRefused)
    account(previousAccount)
  }

  private fun createCertificate() = HeldCertificate.Builder().commonName("wrong-name.invalid")
      .addSubjectAlternativeName("wrong-name.invalid").build()

  private fun server(certificate: HeldCertificate) = MockWebServer().apply {
    val tls = HandshakeCertificates.Builder().heldCertificate(certificate).build()
    useHttps(tls.sslSocketFactory(), false)
    enqueue(MockResponse().setBody("fixture"))
    start(InetAddress.getByName("127.0.0.1"), 0)
  }

  private fun wireRequest(server: MockWebServer, host: String = "localhost"): Request =
      checkNotNull(PorticoTransport.request(server.url("/trust").newBuilder().host(host).build(), "GET", null, null))

  private fun origin(request: Request) = request.url.newBuilder().encodedPath("/").build().toString().removeSuffix("/")

  private fun pin(origin: String, certificate: HeldCertificate) {
    pins[checkNotNull(PorticoServerTrust.key(origin))] =
        PorticoServerTrust.Pin(PorticoServerTrust.fingerprint(certificate.certificate), serverId)
  }

  private fun refused(request: Request, client: OkHttpClient = PorticoTransport.clientFor(request)) {
    try {
      client.newCall(request).execute().use { fail("Unexpected TLS trust for ${request.url.host}:${request.url.port}") }
    } catch (error: IOException) {
      assertTrue("Failure must be an actual certificate refusal", PorticoServerTrust.refusedCertificate(error))
    }
  }

  @Test fun refusesAnUnknownLeafAndAcceptsOnlyItsExactOriginPin() {
    val certificate = createCertificate()
    val expected = Base64.getUrlEncoder().withoutPadding().encodeToString(
        MessageDigest.getInstance("SHA-256").digest(certificate.certificate.encoded))
    assertEquals(expected, PorticoServerTrust.fingerprint(certificate.certificate))
    server(certificate).use { server ->
      val request = wireRequest(server)
      val origin = origin(request)
      refused(request)
      assertEquals(expected, PorticoServerTrust.refused(origin))
      pin(origin, certificate)
      // Policy deliberately accepts this exact pinned leaf even with an untrusted name/issuer.
      PorticoTransport.clientFor(request).newCall(request).execute().use {
        assertEquals(200, it.code)
        assertEquals("fixture", it.body!!.string())
      }
      // Same socket address, different origin host: a pin must not bleed across aliases.
      refused(wireRequest(server, "127.0.0.1"))
    }
  }

  @Test fun wrongLeafAndWrongPortNeverInheritThePin() {
    val certificate = createCertificate()
    server(certificate).use { first ->
      val firstRequest = wireRequest(first)
      pin(origin(firstRequest), createCertificate())
      refused(firstRequest)
      pin(origin(firstRequest), certificate)
      server(certificate).use { second ->
        val secondRequest = wireRequest(second)
        refused(secondRequest)
        assertEquals(PorticoServerTrust.fingerprint(certificate.certificate), PorticoServerTrust.refused(origin(secondRequest)))
      }
      try {
        PorticoServerTrust.trustManager.checkServerTrusted(arrayOf(certificate.certificate), "RSA")
        fail("A context-free trust check must not use an origin pin")
      } catch (_: CertificateException) {
        // No socket/engine origin means no owner pin can grant trust.
      }
    }
  }

  @Test fun accountServiceRemainsSystemTrustOnlyEvenWhenAPinExists() {
    val certificate = createCertificate()
    server(certificate).use { server ->
      val request = wireRequest(server)
      val origin = origin(request)
      pin(origin, certificate)
      account(PorticoServerTrust.key(origin))
      refused(request)
      assertNull(PorticoServerTrust.pinned(origin))
      assertNull(PorticoServerTrust.refused(origin))
    }
  }

  @Test fun proofClientRequiresTheObservedLeafAndItsExactOrigin() {
    val certificate = createCertificate()
    server(certificate).use { server ->
      val request = wireRequest(server)
      val origin = origin(request)
      val fingerprint = PorticoServerTrust.fingerprint(certificate.certificate)
      fun proof(expected: String) = PorticoServerTrust.proofClient(
          OkHttpClient.Builder().followRedirects(false).followSslRedirects(false).callTimeout(15, TimeUnit.SECONDS), origin, expected)
      refused(request, proof(PorticoServerTrust.fingerprint(createCertificate().certificate)))
      refused(wireRequest(server, "127.0.0.1"), proof(fingerprint))
      proof(fingerprint).newCall(request).execute().use { assertEquals(200, it.code) }
      assertNull(PorticoServerTrust.pinned(origin))
    }
  }

  @Test fun failedPersistenceDoesNotReplaceOrForgetAnExistingPin() {
    val certificate = createCertificate()
    val origin = "https://127.0.0.1:23456"
    pin(origin, certificate)
    val original = PorticoServerTrust.pinned(origin)
    assertFalse(PorticoServerTrust.pin(origin, "B".repeat(43), serverId))
    assertEquals(original, PorticoServerTrust.pinned(origin))
    assertFalse(PorticoServerTrust.forgetServer(serverId, emptyList()))
    assertEquals(original, PorticoServerTrust.pinned(origin))
  }
}
