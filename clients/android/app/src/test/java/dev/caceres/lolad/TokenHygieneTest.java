package dev.caceres.lolad;

import static org.junit.Assert.*;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import okhttp3.Request;
import okhttp3.mockwebserver.MockWebServer;
import org.junit.Test;

/**
 * The device token is a bearer credential: it belongs in the Authorization
 * header only. This guards the two ways it usually escapes — a log line or a
 * cache written next to the app's data.
 */
public class TokenHygieneTest {
  @Test
  public void sourcesNeverLogOrCacheTheToken() throws IOException {
    File directory = sourceDirectory();
    File[] files = directory.listFiles((dir, name) -> name.endsWith(".java"));
    assertNotNull(files);
    for (File file : files) {
      String source = new String(Files.readAllBytes(file.toPath()), StandardCharsets.UTF_8);
      String name = file.getName();
      if (name.equals("LocalCache.java") || name.equals("ImageCache.java")) {
        assertFalse(name + " must not touch credentials", source.contains("\"token\""));
        assertFalse(name + " must not touch credentials", source.contains("Bearer"));
      }
      for (String line : source.split("\n")) {
        if (!line.contains("Log.")) continue;
        String lower = line.toLowerCase();
        assertFalse("a log line would leak the credential: " + line, lower.contains("token"));
        assertFalse("a log line would leak the credential: " + line, line.contains("Bearer"));
      }
    }
    String api = new String(
        Files.readAllBytes(new File(directory, "Api.java").toPath()), StandardCharsets.UTF_8);
    assertFalse("never add an HTTP body logger", api.contains("HttpLoggingInterceptor"));
    assertFalse("the token never reaches a URL", api.contains("?token"));
  }

  @Test
  public void apiPutsTheTokenInTheHeaderOnly() throws Exception {
    try (MockWebServer server = new MockWebServer()) {
      Api api = new Api(server.url("/").toString(), "lol_secret_value");
      Request request = api.request("/api/agents?limit=1", null);
      assertFalse(request.url().toString().contains("lol_secret_value"));
      assertFalse(request.url().toString().contains("token"));
      assertEquals("Bearer lol_secret_value", request.header("Authorization"));
      assertEquals(1, request.headers("Authorization").size());
      assertEquals("Bearer lol_secret_value", request.header("authorization"));
    }
  }

  private static File sourceDirectory() {
    File[] candidates = {
      new File("src/main/java/dev/caceres/lolad"),
      new File("app/src/main/java/dev/caceres/lolad")
    };
    for (File candidate : candidates) if (candidate.isDirectory()) return candidate;
    fail("could not find the Java sources from " + new File(".").getAbsolutePath());
    return null;
  }
}
