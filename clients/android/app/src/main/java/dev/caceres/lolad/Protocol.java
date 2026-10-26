package dev.caceres.lolad;

import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import org.json.*;

/** Wire contract helpers without Android dependencies. */
final class Protocol {
  static final String DEFAULT_URL = "https://my-mac.example-tailnet.ts.net:4100";

  static String server(String raw) {
    try {
      URI u = new URI(raw.trim());
      String scheme = u.getScheme();
      if (!("http".equals(scheme) || "https".equals(scheme))
          || u.getHost() == null
          || u.getUserInfo() != null
          || u.getQuery() != null
          || u.getFragment() != null
          || !(u.getPath() == null || u.getPath().isEmpty() || u.getPath().equals("/")))
        throw new IllegalArgumentException();
      return u.toString().replaceAll("/+$", "");
    } catch (Exception e) {
      throw new IllegalArgumentException(
          "Use a full http:// or https:// server address without a path.");
    }
  }

  static String code(String s) {
    s = s.trim().toUpperCase(Locale.ROOT);
    if (!s.matches("[A-Z0-9]{8}"))
      throw new IllegalArgumentException("Enter the 8-character pairing code.");
    return s;
  }

  static String[] pairing(String value) {
    try {
      URI uri = new URI(value);
      if (!"lolad".equals(uri.getScheme()) || !"pair".equals(uri.getHost()))
        throw new IllegalArgumentException();
      Map<String, String> q = new HashMap<>();
      for (String part : uri.getRawQuery().split("&")) {
        String[] kv = part.split("=", 2);
        if (kv.length == 2) q.put(kv[0], URLDecoder.decode(kv[1], StandardCharsets.UTF_8.name()));
      }
      return new String[] {server(q.get("u")), code(q.get("c"))};
    } catch (Exception e) {
      throw new IllegalArgumentException("This QR code isn’t a lolad pairing link.");
    }
  }

  static JSONObject chat(JSONObject mail) {
    JSONObject context = mail.optJSONObject("context");
    JSONObject chat = context == null ? null : context.optJSONObject("userchat");
    return chat == null ? new JSONObject() : chat;
  }

  static String kind(JSONObject m) {
    String k = chat(m).optString("kind", "say");
    return Arrays.asList("say", "update", "deliver", "ask", "reply", "decision").contains(k)
        ? k
        : "say";
  }

  static JSONObject outgoing(String peer, String body, JSONObject parent, String decision)
      throws JSONException {
    JSONObject chat =
        new JSONObject()
            .put("kind", decision != null ? "decision" : parent != null ? "reply" : "say");
    JSONObject out =
        new JSONObject()
            .put("to", peer)
            .put("body", decision == null ? body : decision)
            .put("type", parent == null ? "request" : "response");
    if (parent != null) {
      out.put("in_reply_to", parent.getString("id"));
      String subject = parent.optString("subject").replaceFirst("(?i)^(Re:\\s*)+", "");
      out.put("subject", subject.isEmpty() ? "Re: Message" : "Re: " + subject);
    } else out.put("subject", body.length() > 80 ? body.substring(0, 80) : body);
    if (decision != null) {
      if (parent == null || !"ask".equals(kind(parent)))
        throw new IllegalArgumentException("Question required");
      JSONArray opts = chat(parent).optJSONArray("options");
      boolean valid = false;
      if (opts != null)
        for (int i = 0; i < opts.length(); i++) valid |= decision.equals(opts.optString(i));
      if (!valid) throw new IllegalArgumentException("Unknown option");
      chat.put("decision", decision);
    }
    return out.put("context", new JSONObject().put("userchat", chat));
  }

  static String stripAnsi(String s) {
    return s.replaceAll("\u001B\\][^\u0007]*(?:\u0007|\u001B\\\\)", "")
        .replaceAll("\u001B\\[[0-?]*[ -/]*[@-~]", "")
        .replaceAll("[\\x00-\\x08\\x0B\\x0C\\x0E-\\x1F]", "");
  }

  static String segment(String s) {
    try {
      return URLEncoder.encode(s, "UTF-8").replace("+", "%20");
    } catch (java.io.UnsupportedEncodingException e) {
      throw new AssertionError(e);
    }
  }

  interface EventSink {
    void event(String kind, String data);
  }

  static final class Sse {
    String kind = "message";
    StringBuilder data = new StringBuilder();
    final EventSink sink;

    Sse(EventSink sink) {
      this.sink = sink;
    }

    void line(String line) {
      if (line.isEmpty()) {
        if (data.length() > 0) sink.event(kind, data.substring(0, data.length() - 1));
        kind = "message";
        data.setLength(0);
        return;
      }
      if (line.startsWith(":")) return;
      int p = line.indexOf(':');
      String field = p < 0 ? line : line.substring(0, p), v = p < 0 ? "" : line.substring(p + 1);
      if (v.startsWith(" ")) v = v.substring(1);
      if (field.equals("event")) kind = v;
      if (field.equals("data")) data.append(v).append('\n');
    }
  }
}
