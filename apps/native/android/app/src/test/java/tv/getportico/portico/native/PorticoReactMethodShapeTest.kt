package tv.getportico.portico.native

import com.facebook.react.bridge.ReactMethod
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * React Native takes a method that returns a value for a synchronous one. An asynchronous
 * @ReactMethod that returns anything (an expression body ending in UiThreadUtil.runOnUiThread
 * returns its Boolean) stops the whole module loading, and every screen that touches it crashes.
 */
class PorticoReactMethodShapeTest {
  @Test fun everyAsynchronousReactMethodReturnsNothing() {
    val sources = File("src/main/java/tv/getportico/portico/native").listFiles { f -> f.name.endsWith(".kt") }!!
      .filter { it.readText().contains("@ReactMethod") }
    assertTrue("native module sources were not found", sources.isNotEmpty())
    val classes = sources.flatMap { Regex("""(?m)^(?:internal |open )?class (\w+)""").findAll(it.readText()).map { m -> m.groupValues[1] }.toList() }
    val wrong = classes.flatMap { name ->
      Class.forName("tv.getportico.portico.native.$name", false, javaClass.classLoader).declaredMethods.filter { method ->
        val annotation = method.getAnnotation(ReactMethod::class.java)
        annotation != null && !annotation.isBlockingSynchronousMethod && method.returnType != Void.TYPE
      }.map { "$name.${it.name} returns ${it.returnType.simpleName}" }
    }
    assertEquals(emptyList<String>(), wrong)
  }
}
