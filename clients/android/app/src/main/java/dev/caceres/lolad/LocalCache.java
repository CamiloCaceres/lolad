package dev.caceres.lolad;

import android.content.Context;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * The last known inbox, agents and open-question counts. It is a cache, not a
 * credential store: no token, pairing code or server URL is written here, and
 * it is cleared on disconnect or revocation.
 */
final class LocalCache {
  private static final int VERSION = 2;
  private static final long MAX_BYTES = 2 * 1024 * 1024;
  private final File file;

  LocalCache(Context context) {
    file = new File(context.getCacheDir(), "lolad-inbox.json");
  }

  static final class Snapshot {
    final JSONArray agents, conversations;
    final Map<String, Integer> asks;
    final long savedAt;

    Snapshot(JSONArray agents, JSONArray conversations, Map<String, Integer> asks, long savedAt) {
      this.agents = agents;
      this.conversations = conversations;
      this.asks = asks;
      this.savedAt = savedAt;
    }
  }

  Snapshot read() {
    try {
      if (!file.isFile() || file.length() == 0 || file.length() > MAX_BYTES) return null;
      byte[] bytes;
      try (FileInputStream in = new FileInputStream(file);
          ByteArrayOutputStream out = new ByteArrayOutputStream((int) file.length())) {
        byte[] buffer = new byte[8192];
        int read;
        while ((read = in.read(buffer)) > 0) out.write(buffer, 0, read);
        bytes = out.toByteArray();
      }
      JSONObject root = new JSONObject(new String(bytes, StandardCharsets.UTF_8));
      if (root.optInt("v") != VERSION) return null;
      JSONArray agents = root.optJSONArray("agents");
      JSONArray conversations = root.optJSONArray("conversations");
      if (agents == null || conversations == null) return null;
      Map<String, Integer> asks = new HashMap<>();
      JSONObject map = root.optJSONObject("asks");
      if (map != null) {
        for (java.util.Iterator<String> it = map.keys(); it.hasNext(); ) {
          String peer = it.next();
          asks.put(peer, map.optInt(peer));
        }
      }
      return new Snapshot(agents, conversations, asks, root.optLong("saved_at"));
    } catch (Exception e) {
      return null;
    }
  }

  void write(JSONArray agents, JSONArray conversations, Map<String, Integer> asks) {
    if (agents == null || conversations == null) return;
    try {
      JSONObject map = new JSONObject();
      if (asks != null) for (Map.Entry<String, Integer> entry : asks.entrySet()) map.put(entry.getKey(), entry.getValue());
      JSONObject root =
          new JSONObject()
              .put("v", VERSION)
              .put("agents", agents)
              .put("conversations", conversations)
              .put("asks", map)
              .put("saved_at", System.currentTimeMillis());
      byte[] bytes = root.toString().getBytes(StandardCharsets.UTF_8);
      if (bytes.length > MAX_BYTES) return;
      File temp = new File(file.getPath() + ".tmp");
      try (FileOutputStream out = new FileOutputStream(temp)) {
        out.write(bytes);
        out.getFD().sync();
      }
      if (!temp.renameTo(file)) {
        temp.delete();
      }
    } catch (Exception e) {
      // A cache miss is always recoverable; never surface this to the user.
    }
  }

  void clear() {
    file.delete();
    new File(file.getPath() + ".tmp").delete();
  }
}
