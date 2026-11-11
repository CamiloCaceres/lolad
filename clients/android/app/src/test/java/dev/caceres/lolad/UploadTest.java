package dev.caceres.lolad;

import static org.junit.Assert.*;

import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicReference;
import okhttp3.MediaType;
import okhttp3.MultipartBody;
import okhttp3.RequestBody;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import okio.Buffer;
import org.json.JSONObject;
import org.junit.Test;

/** Multipart shape, streamed file upload and upload error mapping. */
public class UploadTest {
  private static final String ATTACHMENT =
      "{\"storage\":\"provider\",\"id\":\"att_1\",\"filename\":\"Quarterly report.pdf\","
          + "\"content_type\":\"application/pdf\",\"size\":184320,\"digest\":\"sha256:abc\","
          + "\"scan_status\":\"basic_clean\"}";

  @Test
  public void multipartHasExactlyOneFilePartAndEscapesTheFilename() throws Exception {
    RequestBody part = RequestBody.create("hello", MediaType.get("text/plain"));
    MultipartBody body = Api.multipart("a\"b\nc.pdf", part);
    Buffer buffer = new Buffer();
    body.writeTo(buffer);
    String text = buffer.readUtf8();
    assertTrue(text.contains("Content-Disposition: form-data; name=\"file\"; filename=\"a%22b%0Ac.pdf\""));
    assertEquals(1, count(text, "name=\"file\""));
    assertFalse("no raw line break may survive inside the header", text.contains("a\"b\nc.pdf"));
    assertTrue(text.contains("hello"));
  }

  @Test
  public void streamedUploadSendsTheFileWithProgressAndBearerAuth() throws Exception {
    byte[] payload = new byte[512 * 1024];
    for (int i = 0; i < payload.length; i++) payload[i] = (byte) (i % 251);
    File file = File.createTempFile("lolad-upload", ".bin");
    try (FileOutputStream out = new FileOutputStream(file)) {
      out.write(payload);
    }
    try (MockWebServer server = new MockWebServer()) {
      server.enqueue(
          new MockResponse().setResponseCode(201).setBody(ATTACHMENT));
      Api api = new Api(server.url("/").toString(), "lol_test");
      List<long[]> progress = new ArrayList<>();
      RequestBody part =
          Api.fileBody(
              file,
              "application/pdf",
              (written, total) -> progress.add(new long[] {written, total}));
      assertEquals(payload.length, part.contentLength());
      JSONObject result =
          api.upload("iris", "Quarterly report.pdf", "application/pdf", part, new AtomicReference<>());
      assertEquals("att_1", result.getString("id"));

      RecordedRequest request = server.takeRequest();
      assertEquals("POST", request.getMethod());
      assertEquals("/api/mail/attachments?peer=iris", request.getPath());
      assertEquals("Bearer lol_test", request.getHeader("Authorization"));
      assertTrue(request.getHeader("Content-Type").startsWith("multipart/form-data; boundary="));
      String body = new String(request.getBody().readByteArray(), StandardCharsets.ISO_8859_1);
      assertTrue(body.contains("Content-Disposition: form-data; name=\"file\"; filename=\"Quarterly report.pdf\""));
      assertTrue(body.contains("Content-Type: application/pdf"));
      int start = body.indexOf("\r\n\r\n") + 4;
      int end = body.lastIndexOf("\r\n--");
      byte[] sent = body.substring(start, end).getBytes(StandardCharsets.ISO_8859_1);
      assertArrayEquals(payload, sent);

      assertFalse(progress.isEmpty());
      long previous = -1;
      for (long[] step : progress) {
        assertTrue("progress never goes backwards", step[0] >= previous);
        assertEquals(payload.length, step[1]);
        previous = step[0];
      }
      assertEquals(payload.length, previous);
    } finally {
      file.delete();
    }
  }

  @Test
  public void uploadErrorsMapToHumanMessages() throws Exception {
    try (MockWebServer server = new MockWebServer()) {
      Api api = new Api(server.url("/").toString(), "lol_test");
      server.enqueue(new MockResponse().setResponseCode(413));
      try {
        api.upload(
            "iris",
            "huge.bin",
            "application/octet-stream",
            RequestBody.create(new byte[] {1}, MediaType.get("application/octet-stream")),
            new AtomicReference<>());
        fail("413 must fail");
      } catch (Api.Failure failure) {
        assertEquals(413, failure.status);
        assertTrue(failure.getMessage().contains("500 MB"));
      }
      server.enqueue(new MockResponse().setResponseCode(404));
      try {
        api.upload(
            "iris",
            "x.txt",
            "text/plain",
            RequestBody.create("x", MediaType.get("text/plain")),
            new AtomicReference<>());
        fail("404 must fail");
      } catch (Api.Failure failure) {
        assertEquals(404, failure.status);
        assertTrue(failure.getMessage().contains("agent"));
      }
    }
  }

  private static int count(String haystack, String needle) {
    int found = 0;
    int from = 0;
    while ((from = haystack.indexOf(needle, from)) >= 0) {
      found++;
      from += needle.length();
    }
    return found;
  }
}
