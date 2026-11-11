package dev.caceres.lolad;

import android.text.format.DateUtils;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/** Immutable, already-parsed rows. All of this is built on the IO executor. */
final class Model {
  private Model() {}

  /** 64-bit stable id for RecyclerView; mail ids and peers are stable strings. */
  static long stableId(String value) {
    long hash = 1125899906842597L;
    for (int i = 0; i < value.length(); i++) hash = 31 * hash + value.charAt(i);
    return hash;
  }

  enum Tone {
    AWAKE,
    ATTENTION,
    ERROR,
    MUTED
  }

  static String stamp(String iso) {
    if (iso == null || iso.isEmpty()) return "No activity yet";
    try {
      return DateUtils.getRelativeTimeSpanString(
              Instant.parse(iso).toEpochMilli(),
              System.currentTimeMillis(),
              DateUtils.MINUTE_IN_MILLIS)
          .toString();
    } catch (Exception e) {
      return "No activity yet";
    }
  }

  static final class Message {
    final String id, inReplyTo, body, subject, kind, byline, choice;
    final boolean mine, answered, unread, pending;
    final List<String> options;
    final List<Attachment> attachments;

    Message(
        String id,
        String inReplyTo,
        String body,
        String subject,
        String kind,
        String byline,
        String choice,
        boolean mine,
        boolean answered,
        boolean unread,
        List<String> options) {
      this(
          id,
          inReplyTo,
          body,
          subject,
          kind,
          byline,
          choice,
          mine,
          answered,
          unread,
          options,
          Collections.emptyList(),
          false);
    }

    Message(
        String id,
        String inReplyTo,
        String body,
        String subject,
        String kind,
        String byline,
        String choice,
        boolean mine,
        boolean answered,
        boolean unread,
        List<String> options,
        List<Attachment> attachments,
        boolean pending) {
      this.id = id;
      this.inReplyTo = inReplyTo;
      this.body = body;
      this.subject = subject;
      this.kind = kind;
      this.byline = byline;
      this.choice = choice;
      this.mine = mine;
      this.answered = answered;
      this.unread = unread;
      this.options = options;
      this.attachments =
          attachments == null || attachments.isEmpty()
              ? Collections.emptyList()
              : Collections.unmodifiableList(new ArrayList<>(attachments));
      this.pending = pending;
    }

    /** Local echo while a send is in flight; the server row replaces it. */
    static Message optimistic(
        String id, String subject, String body, List<Attachment> attachments) {
      return new Message(
          id,
          "",
          body,
          subject,
          "say",
          "You · Sending…",
          "",
          true,
          false,
          false,
          Collections.emptyList(),
          attachments,
          true);
    }

    boolean isAsk() {
      return "ask".equals(kind);
    }

    String markdownKey() {
      return MarkdownCache.key(id, body, mine);
    }

    boolean hasAttachments() {
      return !attachments.isEmpty();
    }
  }

  static List<Message> messages(JSONArray thread, String me, String peerDisplay) {
    List<Message> out = new ArrayList<>();
    if (thread == null || thread.length() == 0) return out;
    int n = thread.length();
    JSONObject[] raw = new JSONObject[n];
    boolean[] mine = new boolean[n];
    boolean[] answered = new boolean[n];
    String[] choice = new String[n];
    String[] kinds = new String[n];
    for (int i = 0; i < n; i++) {
      JSONObject mail = thread.optJSONObject(i);
      if (mail == null) continue;
      raw[i] = mail;
      mine[i] = mail.optString("from_name").equals(me);
      kinds[i] = Protocol.kind(mail);
    }
    // Existing behaviour: an ask is answered by the first later outgoing
    // message, and the chosen decision only when that message replies to it.
    JSONObject laterMine = null;
    for (int i = n - 1; i >= 0; i--) {
      if (raw[i] == null) continue;
      if (mine[i]) {
        laterMine = raw[i];
      } else if (kinds[i].equals("ask")) {
        answered[i] = laterMine != null;
        choice[i] =
            laterMine != null && raw[i].optString("id").equals(laterMine.optString("in_reply_to"))
                ? Protocol.chat(laterMine).optString("decision")
                : "";
      }
    }
    for (int i = 0; i < n; i++) {
      JSONObject mail = raw[i];
      if (mail == null) continue;
      String kind = kinds[i];
      String who = mine[i] ? "You" : peerDisplay;
      String suffix =
          kind.equals("deliver")
              ? " · Deliverable"
              : kind.equals("ask")
                  ? " · Question"
                  : kind.equals("decision") ? " · Decision" : "";
      List<String> options = new ArrayList<>();
      JSONArray array = Protocol.chat(mail).optJSONArray("options");
      if (array != null)
        for (int j = 0; j < array.length(); j++) {
          String option = array.optString(j);
          if (!option.isEmpty()) options.add(option);
        }
      out.add(
          new Message(
              mail.optString("id"),
              mail.optString("in_reply_to"),
              mail.optString("body"),
              mail.optString("subject"),
              kind,
              who + suffix + " · " + stamp(mail.optString("created_at")),
              choice[i],
              mine[i],
              answered[i],
              !mine[i] && !"read".equals(mail.optString("status")),
              Collections.unmodifiableList(options),
              Attachment.list(mail.optJSONArray("attachments")),
              false));
    }
    return out;
  }

  static final class Conversation {
    final String peer, display, preview, lastAt, stamp;
    final int openAsks, unread;

    Conversation(
        String peer, String display, String preview, String lastAt, int openAsks, int unread) {
      this.peer = peer;
      this.display = display;
      this.preview = preview;
      this.lastAt = lastAt;
      this.stamp = stamp(lastAt);
      this.openAsks = openAsks;
      this.unread = unread;
    }
  }

  static List<Conversation> conversations(JSONArray source, Map<String, Integer> asks) {
    List<Conversation> out = new ArrayList<>();
    if (source == null) return out;
    for (int i = 0; i < source.length(); i++) {
      JSONObject conversation = source.optJSONObject(i);
      if (conversation == null) continue;
      String peer = conversation.optString("peer");
      if (peer.isEmpty()) continue;
      JSONObject last = conversation.optJSONObject("last");
      String preview = last == null ? "" : last.optString("body");
      if (preview.trim().isEmpty()) preview = filePreview(last);
      out.add(
          new Conversation(
              peer,
              conversation.optString("display", peer),
              preview,
              conversation.optString("last_at"),
              asks == null ? 0 : asks.getOrDefault(peer, 0),
              conversation.optInt("unread")));
    }
    out.sort(
        (a, b) -> {
          int waiting = Boolean.compare(b.openAsks > 0, a.openAsks > 0);
          if (waiting != 0) return waiting;
          int newest = b.lastAt.compareTo(a.lastAt);
          return newest != 0 ? newest : a.peer.compareTo(b.peer);
        });
    return out;
  }

  /** An inbox row with no text still says what arrived. */
  static String filePreview(JSONObject mail) {
    if (mail == null) return "";
    List<Attachment> files = Attachment.list(mail.optJSONArray("attachments"));
    if (files.isEmpty()) return "";
    if (files.size() == 1) return files.get(0).filename;
    return files.size() + " files";
  }

  static final class Agent {
    final String name, display, stateLabel, runtimeLine, activity, lastActivity;
    final int openAsks, unread;
    final Tone tone;

    Agent(
        String name,
        String display,
        String stateLabel,
        String runtimeLine,
        String activity,
        String lastActivity,
        int openAsks,
        int unread,
        Tone tone) {
      this.name = name;
      this.display = display;
      this.stateLabel = stateLabel;
      this.runtimeLine = runtimeLine;
      this.activity = activity;
      this.lastActivity = lastActivity;
      this.openAsks = openAsks;
      this.unread = unread;
      this.tone = tone;
    }
  }

  static String statusLabel(String state) {
    switch (state) {
      case "busy":
        return "Working";
      case "starting":
        return "Starting";
      case "idle":
        return "Idle";
      case "waiting":
        return "Waiting";
      case "stopped":
        return "Stopped";
      case "crashed":
        return "Crashed";
      case "failed":
        return "Failed";
      default:
        return "Running";
    }
  }

  static Tone tone(String state) {
    switch (state) {
      case "busy":
        return Tone.AWAKE;
      case "waiting":
      case "starting":
        return Tone.ATTENTION;
      case "crashed":
      case "failed":
        return Tone.ERROR;
      default:
        return Tone.MUTED;
    }
  }

  static List<Agent> agents(JSONArray source, Map<String, Integer> asks, Map<String, Integer> unread) {
    List<Agent> out = new ArrayList<>();
    if (source == null) return out;
    for (int i = 0; i < source.length(); i++) {
      JSONObject agent = source.optJSONObject(i);
      if (agent == null) continue;
      String name = agent.optString("name");
      if (name.isEmpty()) continue;
      String state = agent.optString("state", "unknown");
      if (!java.util.Arrays.asList(
              "stopped", "starting", "idle", "busy", "waiting", "crashed", "failed", "unknown")
          .contains(state)) state = "unknown";
      JSONObject runtime = agent.optJSONObject("runtime");
      String runtimeLine =
          runtime == null
              ? ""
              : runtime.optString("harness") + " · " + runtime.optString("model", "Default model");
      out.add(
          new Agent(
              name,
              agent.optString("display", name),
              statusLabel(state),
              runtimeLine,
              "Active " + stamp(agent.optString("last_activity")),
              agent.optString("last_activity"),
              asks == null ? 0 : asks.getOrDefault(name, 0),
              unread == null ? agent.optInt("unread") : unread.getOrDefault(name, 0),
              tone(state)));
    }
    out.sort(
        (a, b) -> {
          int waiting = Boolean.compare(b.openAsks > 0, a.openAsks > 0);
          if (waiting != 0) return waiting;
          int newest = b.lastActivity.compareTo(a.lastActivity);
          return newest != 0 ? newest : a.name.compareTo(b.name);
        });
    return out;
  }
}
