package dev.caceres.lolad;

import static org.junit.Assert.*;

import java.util.*;
import org.json.*;
import org.junit.Test;

public class ProtocolTest {
  @Test
  public void urls() {
    assertEquals("https://host:4100", Protocol.server(" https://host:4100/ "));
    assertEquals("http://10.0.2.2:4193", Protocol.server("http://10.0.2.2:4193"));
    for (String bad :
        new String[] {
          "host:4100",
          "https://u:p@host",
          "https://host/path",
          "file:///tmp/x",
          "https://host?token=secret"
        })
      try {
        Protocol.server(bad);
        fail(bad);
      } catch (IllegalArgumentException expected) {
      }
  }

  @Test
  public void pairLink() {
    assertArrayEquals(
        new String[] {"https://host:4100", "ABCD2345"},
        Protocol.pairing("lolad://pair?u=https%3A%2F%2Fhost%3A4100&c=abcd2345"));
    for (String bad :
        new String[] {
          "https://pair?u=https://h&c=ABCD2345",
          "lolad://pair?u=https://host",
          "lolad://pair?u=https://host&c=ABC"
        })
      try {
        Protocol.pairing(bad);
        fail();
      } catch (IllegalArgumentException expected) {
      }
  }

  @Test
  public void kinds() throws Exception {
    for (String kind : new String[] {"say", "ask", "update", "deliver", "reply", "decision"})
      assertEquals(kind, Protocol.kind(mail(kind)));
    assertEquals("say", Protocol.kind(mail("future")));
    assertEquals("say", Protocol.kind(new JSONObject()));
  }

  static JSONObject mail(String kind) throws Exception {
    return new JSONObject()
        .put("id", "msg_ask")
        .put("subject", "Deploy?")
        .put(
            "context",
            new JSONObject()
                .put(
                    "userchat",
                    new JSONObject()
                        .put("kind", kind)
                        .put("options", new JSONArray().put("Deploy").put("Wait"))));
  }

  @Test
  public void decisionMatchesWebInbox() throws Exception {
    JSONObject result = Protocol.outgoing("iris", "", mail("ask"), "Wait");
    assertEquals("iris", result.getString("to"));
    assertEquals("Wait", result.getString("body"));
    assertEquals("response", result.getString("type"));
    assertEquals("msg_ask", result.getString("in_reply_to"));
    assertEquals("Re: Deploy?", result.getString("subject"));
    assertEquals("decision", Protocol.chat(result).getString("kind"));
    assertEquals("Wait", Protocol.chat(result).getString("decision"));
    try {
      Protocol.outgoing("iris", "", mail("ask"), "Wrong");
      fail();
    } catch (IllegalArgumentException expected) {
    }
  }

  @Test
  public void replyAndNewMail() throws Exception {
    JSONObject reply = Protocol.outgoing("iris", "Hello", mail("say"), null);
    assertEquals("reply", Protocol.kind(reply));
    assertEquals("msg_ask", reply.getString("in_reply_to"));
    JSONObject first = Protocol.outgoing("iris", "Hello", null, null);
    assertEquals("say", Protocol.kind(first));
    assertFalse(first.has("in_reply_to"));
  }

  @Test
  public void sseCommentsMultilineAndReset() {
    List<String> events = new ArrayList<>();
    Protocol.Sse parser = new Protocol.Sse((kind, data) -> events.add(kind + "|" + data));
    for (String line :
        new String[] {
          ": keepalive", "", "event: mail", "data: one", "data:two", "id:42", "", "data: next", ""
        }) parser.line(line);
    assertEquals(Arrays.asList("mail|one\ntwo", "message|next"), events);
  }

  @Test
  public void ansiIsStripped() {
    assertEquals("hello\nworld", Protocol.stripAnsi("\u001B[32mhello\u001B[0m\nworld"));
  }

  @Test
  public void storageScopedAndCleared() {
    class Memory implements Session.Storage {
      String server = "", token = "";

      public String get(String k) {
        return k.equals("server") ? server : token;
      }

      public void save(String u, String t) {
        server = u;
        token = t;
      }

      public void clear() {
        server = "";
        token = "";
      }
    }
    Memory m = new Memory();
    Session s = new Session(m);
    assertFalse(s.connected());
    s.connect("https://host/", "lol_test");
    assertEquals("https://host", s.server());
    assertTrue(s.connected());
    try {
      s.connect("https://other", "bad");
      fail();
    } catch (IllegalArgumentException expected) {
    }
    assertEquals("https://host", s.server());
    s.disconnect();
    assertEquals("", s.token());
    assertFalse(s.connected());
  }
}
