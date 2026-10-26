package dev.caceres.lolad;

import static org.junit.Assert.*;

import java.util.concurrent.*;
import okhttp3.mockwebserver.*;
import org.junit.Test;

public class ApiTest {
  @Test
  public void bearerOnJsonAndSse() throws Exception {
    try (MockWebServer server = new MockWebServer()) {
      server.enqueue(new MockResponse().setBody("[]"));
      Api api = new Api(server.url("/").toString(), "lol_test");
      api.array("/api/agents");
      assertEquals("Bearer lol_test", server.takeRequest().getHeader("Authorization"));
      server.enqueue(
          new MockResponse()
              .setHeader("Content-Type", "text/event-stream")
              .setBody("event: mail\ndata: {\"id\":1}\n\n"));
      CountDownLatch received = new CountDownLatch(1);
      api.stream((k, d) -> received.countDown(), e -> {}, () -> {});
      assertTrue(received.await(5, TimeUnit.SECONDS));
      assertEquals("Bearer lol_test", server.takeRequest().getHeader("Authorization"));
    }
  }

  @Test
  public void revokedAndRateLimit() throws Exception {
    try (MockWebServer server = new MockWebServer()) {
      Api api = new Api(server.url("/").toString(), "lol_test");
      server.enqueue(new MockResponse().setResponseCode(401));
      try {
        api.array("/api/agents");
        fail();
      } catch (Api.Failure e) {
        assertEquals(401, e.status);
        assertTrue(e.getMessage().contains("revoked"));
      }
      server.enqueue(new MockResponse().setResponseCode(429).setHeader("Retry-After", "17"));
      try {
        api.array("/api/agents");
        fail();
      } catch (Api.Failure e) {
        assertEquals(17, e.retryAfter);
      }
    }
  }
}
