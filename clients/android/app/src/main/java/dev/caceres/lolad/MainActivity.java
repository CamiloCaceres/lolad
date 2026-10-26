package dev.caceres.lolad;

import android.content.*;
import android.content.res.Configuration;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.os.*;
import android.text.*;
import android.text.format.DateUtils;
import android.view.*;
import android.widget.*;
import androidx.activity.ComponentActivity;
import androidx.activity.OnBackPressedCallback;
import androidx.activity.result.ActivityResultLauncher;
import androidx.core.view.*;
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout;
import com.google.android.material.button.MaterialButton;
import com.google.android.material.dialog.MaterialAlertDialogBuilder;
import com.google.android.material.snackbar.Snackbar;
import com.google.android.material.textfield.*;
import com.journeyapps.barcodescanner.*;
import io.noties.markwon.Markwon;
import java.util.*;
import java.util.concurrent.*;
import okhttp3.Call;
import org.json.*;

public final class MainActivity extends ComponentActivity {
  final Handler handler = new Handler(Looper.getMainLooper());
  final ExecutorService work = Executors.newSingleThreadExecutor();
  final ArrayList<String> history = new ArrayList<>();
  Session session;
  Api api;
  Markwon markdown;
  Call stream;
  LinearLayout root, body, threadRows;
  TextView banner;
  ScrollView threadScroll;
  SwipeRefreshLayout swipe;
  EditText draftField, urlField, codeField, tokenField;
  MaterialButton sendButton;
  JSONArray agents = new JSONArray(), conversations = new JSONArray(), messages = new JSONArray();
  String me = "", peer = "", screen = "inbox", draft = "", signature = "", terminal = "";
  volatile boolean foreground;
  boolean loading, sending, rateLimited;
  volatile int revision, connection;
  int ink, muted, paper, backdrop;
  long retryAt;
  int reconnect = 1000;
  ActivityResultLauncher<ScanOptions> scanner;
  final Runnable poll =
      new Runnable() {
        public void run() {
          if (foreground && api != null) {
            refresh();
            handler.postDelayed(this, 10000);
          }
        }
      };
  final Runnable reconnectStream = () -> startStream();

  @Override
  public void onCreate(Bundle saved) {
    super.onCreate(saved);
    session = new Session(new KeystoreStorage(this));
    markdown =
        Markwon.builder(this)
            .usePlugin(io.noties.markwon.ext.tables.TablePlugin.create(this))
            .usePlugin(io.noties.markwon.ext.strikethrough.StrikethroughPlugin.create())
            .build();
    scanner =
        registerForActivityResult(
            new ScanContract(),
            result -> {
              if (result.getContents() != null) pairLink(result.getContents());
            });
    boolean dark =
        (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK)
            == Configuration.UI_MODE_NIGHT_YES;
    ink = color(com.google.android.material.R.attr.colorOnSurface);
    muted = color(com.google.android.material.R.attr.colorOnSurfaceVariant);
    paper = color(com.google.android.material.R.attr.colorSurface);
    backdrop = color(android.R.attr.colorBackground);
    WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
    WindowCompat.getInsetsController(getWindow(), getWindow().getDecorView())
        .setAppearanceLightStatusBars(!dark);
    WindowCompat.getInsetsController(getWindow(), getWindow().getDecorView())
        .setAppearanceLightNavigationBars(!dark);
    getOnBackPressedDispatcher()
        .addCallback(
            this,
            new OnBackPressedCallback(true) {
              public void handleOnBackPressed() {
                WindowInsetsCompat insets = ViewCompat.getRootWindowInsets(root);
                if (insets != null && insets.isVisible(WindowInsetsCompat.Type.ime())) {
                  WindowCompat.getInsetsController(getWindow(), root)
                      .hide(WindowInsetsCompat.Type.ime());
                  return;
                }
                rememberDraft();
                if (!history.isEmpty()) {
                  String[] previous = history.remove(history.size() - 1).split("\\|", -1);
                  peer = previous[0];
                  screen = previous[1];
                  draw();
                  refresh();
                } else if (!peer.isEmpty()) {
                  peer = "";
                  screen = "inbox";
                  draw();
                  refresh();
                } else {
                  setEnabled(false);
                  getOnBackPressedDispatcher().onBackPressed();
                  setEnabled(true);
                }
              }
            });
    if (saved != null) {
      ArrayList<String> prior = saved.getStringArrayList("history");
      if (prior != null) history.addAll(prior);
      screen = saved.getString("screen", "inbox");
      peer = saved.getString("peer", "");
      draft = saved.getString("draft", "");
    }
    if (session.connected()) api = new Api(session.server(), session.token());
    else screen = "connect";
    draw();
    if (getIntent().getData() != null) pairLink(getIntent().getData().toString());
  }

  @Override
  protected void onNewIntent(Intent intent) {
    super.onNewIntent(intent);
    setIntent(intent);
    if (intent.getData() != null) pairLink(intent.getData().toString());
  }

  @Override
  protected void onSaveInstanceState(Bundle out) {
    rememberDraft();
    out.putStringArrayList("history", history);
    out.putString("screen", screen);
    out.putString("peer", peer);
    out.putString("draft", draft);
    super.onSaveInstanceState(out);
  }

  @Override
  protected void onStart() {
    super.onStart();
    foreground = true;
    if (api != null) {
      handler.post(poll);
      startStream();
    }
  }

  @Override
  protected void onStop() {
    foreground = false;
    rememberDraft();
    handler.removeCallbacks(poll);
    handler.removeCallbacks(reconnectStream);
    if (stream != null) {
      stream.cancel();
      stream = null;
    }
    super.onStop();
  }

  @Override
  protected void onDestroy() {
    work.shutdownNow();
    super.onDestroy();
  }

  int dp(int value) {
    return Math.round(value * getResources().getDisplayMetrics().density);
  }

  int color(int attr) {
    android.util.TypedValue v = new android.util.TypedValue();
    getTheme().resolveAttribute(attr, v, true);
    return v.data;
  }

  LinearLayout column() {
    LinearLayout v = new LinearLayout(this);
    v.setOrientation(LinearLayout.VERTICAL);
    return v;
  }

  TextView text(String value, int sp) {
    TextView v = new TextView(this);
    v.setText(value);
    v.setTextSize(sp);
    v.setTextColor(ink);
    v.setLineSpacing(dp(3), 1);
    return v;
  }

  TextView quiet(String value) {
    TextView v = text(value, 12);
    v.setTextColor(muted);
    return v;
  }

  GradientDrawable surface(int c) {
    GradientDrawable d = new GradientDrawable();
    d.setColor(c);
    d.setCornerRadius(dp(16));
    return d;
  }

  MaterialButton button(String label, Runnable action) {
    MaterialButton b =
        new MaterialButton(
            this, null, com.google.android.material.R.attr.materialButtonOutlinedStyle);
    b.setText(label);
    b.setMinHeight(dp(48));
    b.setBackgroundTintList(
        android.content.res.ColorStateList.valueOf(android.graphics.Color.TRANSPARENT));
    b.setStrokeWidth(0);
    b.setTextColor(ink);
    b.setOnClickListener(v -> action.run());
    return b;
  }

  MaterialButton primary(String label, Runnable action) {
    MaterialButton b = new MaterialButton(this);
    b.setText(label);
    b.setMinHeight(dp(48));
    b.setOnClickListener(v -> action.run());
    return b;
  }

  void heading(LinearLayout parent, String title, String detail) {
    TextView t = text(title, 24);
    t.setTypeface(null, Typeface.BOLD);
    parent.addView(t);
    TextView d = text(detail, 14);
    d.setTextColor(muted);
    d.setPadding(0, dp(8), 0, dp(20));
    parent.addView(d);
  }

  void note(String message) {
    if (root != null) Snackbar.make(root, message, Snackbar.LENGTH_LONG).show();
  }

  void rememberDraft() {
    if (draftField != null) draft = draftField.getText().toString();
  }

  void actions(LinearLayout parent, View... buttons) {
    ActionRows rows = new ActionRows(this, dp(8));
    for (View button : buttons)
      rows.addView(button, new android.view.ViewGroup.LayoutParams(-2, -2));
    parent.addView(rows, new LinearLayout.LayoutParams(-1, -2));
  }

  void draw() {
    revision++;
    signature = "";
    draftField = null;
    sendButton = null;
    threadRows = null;
    swipe = null;
    root = column();
    root.setBackgroundColor(backdrop);
    root.setPadding(dp(16), 0, dp(16), 0);
    setContentView(root);
    ViewCompat.setOnApplyWindowInsetsListener(
        root,
        (v, insets) -> {
          androidx.core.graphics.Insets bars =
              insets.getInsets(
                  WindowInsetsCompat.Type.systemBars() | WindowInsetsCompat.Type.displayCutout());
          int bottom =
              Math.max(bars.bottom, insets.getInsets(WindowInsetsCompat.Type.ime()).bottom);
          root.setPadding(bars.left + dp(16), bars.top, bars.right + dp(16), bottom);
          return insets;
        });
    LinearLayout bar = new LinearLayout(this);
    bar.setGravity(Gravity.CENTER_VERTICAL);
    if (!peer.isEmpty())
      bar.addView(button("Back", () -> getOnBackPressedDispatcher().onBackPressed()));
    TextView title = text(peer.isEmpty() ? "lolad" : display(peer), 24);
    title.setTypeface(null, Typeface.BOLD);
    title.setMaxLines(1);
    title.setEllipsize(TextUtils.TruncateAt.END);
    bar.addView(title, new LinearLayout.LayoutParams(0, -2, 1));
    if (api != null)
      bar.addView(
          button(
              "Disconnect",
              () ->
                  new MaterialAlertDialogBuilder(this)
                      .setTitle("Disconnect this device?")
                      .setMessage("This removes the saved device token from this phone.")
                      .setNegativeButton("Cancel", null)
                      .setPositiveButton("Disconnect", (d, w) -> disconnect(""))
                      .show()));
    root.addView(bar);
    banner = quiet(api == null ? "Your agents, wherever you are." : "Connecting…");
    banner.setPadding(0, dp(4), 0, dp(8));
    banner.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
    root.addView(banner);
    body = column();
    root.addView(body, new LinearLayout.LayoutParams(-1, 0, 1));
    if (api == null) {
      connectView();
      return;
    }
    if (peer.isEmpty()) {
      actions(
          body,
          button(
              screen.equals("inbox") ? "Inbox · selected" : "Inbox",
              () -> {
                screen = "inbox";
                draw();
                renderList();
                refresh();
              }),
          button(
              screen.equals("agents") ? "Agents · selected" : "Agents",
              () -> {
                screen = "agents";
                draw();
                renderList();
                refresh();
              }),
          button("Refresh", this::userRefresh));
      swipe = new SwipeRefreshLayout(this);
      ScrollView scroll = new ScrollView(this);
      threadRows = column();
      scroll.addView(threadRows);
      swipe.addView(scroll);
      swipe.setOnRefreshListener(this::userRefresh);
      body.addView(swipe, new LinearLayout.LayoutParams(-1, 0, 1));
      renderList();
    } else if (screen.equals("chat")) chatView();
    else if (screen.equals("agent")) agentView();
    else terminalView();
  }

  EditText field(LinearLayout parent, String label, int input) {
    TextInputLayout wrap = new TextInputLayout(this);
    wrap.setHint(label);
    wrap.setBoxBackgroundMode(TextInputLayout.BOX_BACKGROUND_OUTLINE);
    TextInputEditText edit = new TextInputEditText(wrap.getContext());
    edit.setTextSize(16);
    edit.setInputType(input);
    edit.setTypeface(Typeface.DEFAULT);
    edit.setMinHeight(dp(56));
    wrap.addView(edit);
    LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(-1, -2);
    p.bottomMargin = dp(16);
    parent.addView(wrap, p);
    return edit;
  }

  void connectView() {
    ScrollView scroll = new ScrollView(this);
    LinearLayout form = column();
    scroll.addView(form);
    body.addView(scroll);
    heading(
        form,
        "Connect to your agents",
        "Use the Devices page on your dashboard to create a pairing code. Make sure Tailscale is"
            + " connected.");
    urlField =
        field(
            form,
            "Server address",
            android.text.InputType.TYPE_CLASS_TEXT
                | android.text.InputType.TYPE_TEXT_VARIATION_URI);
    urlField.setText(session.server().isEmpty() ? Protocol.DEFAULT_URL : session.server());
    codeField =
        field(
            form,
            "8-character pairing code",
            android.text.InputType.TYPE_CLASS_TEXT
                | android.text.InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS);
    codeField.setFilters(new InputFilter[] {new InputFilter.LengthFilter(8)});
    form.addView(primary("Pair device", this::pair));
    form.addView(
        button(
            "Scan dashboard QR code",
            () ->
                scanner.launch(
                    new ScanOptions()
                        .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                        .setPrompt("Scan the lolad Devices pairing QR")
                        .setBeepEnabled(false)
                        .setOrientationLocked(false))));
    TextView fallback = text("Already have a device token?", 14);
    fallback.setPadding(0, dp(24), 0, dp(8));
    form.addView(fallback);
    tokenField =
        field(
            form,
            "Device token (manual fallback)",
            android.text.InputType.TYPE_CLASS_TEXT
                | android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD);
    form.addView(button("Connect with token", () -> connectToken()));
  }

  void pairLink(String link) {
    getIntent().setData(null);
    try {
      String[] pair = Protocol.pairing(link);
      if (api != null) {
        note("Disconnect before pairing another device.");
        return;
      }
      urlField.setText(pair[0]);
      codeField.setText(pair[1]);
      note("Pairing link ready. Tap Pair device.");
    } catch (Exception e) {
      note(e.getMessage());
    }
  }

  void pair() {
    if (loading) return;
    if (System.currentTimeMillis() < retryAt) {
      note("Wait before trying another pairing code.");
      return;
    }
    rateLimited = false;
    final String url, code;
    try {
      url = Protocol.server(urlField.getText().toString());
      code = Protocol.code(codeField.getText().toString());
    } catch (Exception e) {
      note(e.getMessage());
      return;
    }
    loading = true;
    banner.setText("Pairing this device…");
    work.execute(
        () -> {
          try {
            JSONObject result =
                new Api(url, "")
                    .post(
                        "/api/pair",
                        new JSONObject()
                            .put("code", code)
                            .put("device", Build.MODEL)
                            .put("kind", "android"));
            handler.post(
                () -> {
                  loading = false;
                  try {
                    establish(url, result.getString("token"));
                  } catch (Exception e) {
                    handle(e);
                  }
                });
          } catch (Exception e) {
            handler.post(
                () -> {
                  loading = false;
                  handle(e);
                });
          }
        });
  }

  void connectToken() {
    if (loading) return;
    if (System.currentTimeMillis() < retryAt) {
      note("Wait before trying another token.");
      return;
    }
    rateLimited = false;
    try {
      String url = Protocol.server(urlField.getText().toString()),
          token = tokenField.getText().toString().trim();
      if (!token.matches("lol_[A-Za-z0-9_-]+"))
        throw new IllegalArgumentException("Paste a lol_ device token.");
      loading = true;
      Api candidate = new Api(url, token);
      banner.setText("Checking device…");
      work.execute(
          () -> {
            try {
              candidate.object("/api/me");
              handler.post(
                  () -> {
                    loading = false;
                    try {
                      establish(url, token);
                    } catch (Exception e) {
                      handle(e);
                    }
                  });
            } catch (Exception e) {
              handler.post(
                  () -> {
                    loading = false;
                    handle(e);
                  });
            }
          });
    } catch (Exception e) {
      handle(e);
    }
  }

  void establish(String url, String token) {
    loading = true;
    // Key creation and durable encryption can involve hardware; keep it off the UI thread.
    work.execute(
        () -> {
          try {
            session.connect(url, token);
            handler.post(
                () -> {
                  if (isDestroyed()) return;
                  loading = false;
                  rateLimited = false;
                  retryAt = 0;
                  connection++;
                  api = new Api(url, token);
                  peer = "";
                  screen = "inbox";
                  draft = "";
                  draw();
                  handler.removeCallbacks(poll);
                  if (foreground) {
                    handler.post(poll);
                    startStream();
                  }
                });
          } catch (Exception e) {
            handler.post(
                () -> {
                  loading = false;
                  handle(e);
                });
          }
        });
  }

  void disconnect(String reason) {
    connection++;
    if (stream != null) {
      stream.cancel();
      stream = null;
    }
    handler.removeCallbacks(poll);
    handler.removeCallbacks(reconnectStream);
    api = null;
    session.disconnect();
    history.clear();
    agents = new JSONArray();
    conversations = new JSONArray();
    messages = new JSONArray();
    peer = "";
    me = "";
    screen = "connect";
    draft = "";
    sending = false;
    loading = false;
    draw();
    if (!reason.isEmpty()) banner.setText(reason);
  }

  void handle(Exception e) {
    if (e instanceof Api.Failure) {
      Api.Failure f = (Api.Failure) e;
      if (f.status == 401 && api != null) {
        disconnect(f.getMessage());
        return;
      }
      if (f.status == 429) {
        rateLimited = true;
        retryAt = System.currentTimeMillis() + f.retryAfter * 1000;
        handler.removeCallbacks(reconnectStream);
      }
    }
    String error =
        e instanceof java.io.IOException && !(e instanceof Api.Failure)
            ? "Can’t reach the server — is Tailscale on?"
            : e.getMessage();
    if (e instanceof JSONException) error = "The server response couldn’t be read. Try Refresh.";
    if (error == null) error = "Couldn’t load this view. Try Refresh.";
    banner.setText(error);
    note(error);
  }

  void startStream() {
    if (rateLimited
        || !foreground
        || api == null
        || stream != null
        || System.currentTimeMillis() < retryAt) return;
    int epoch = connection;
    stream =
        api.stream(
            (kind, data) ->
                handler.post(
                    () -> {
                      if (epoch == connection && foreground) refresh();
                    }),
            e ->
                handler.post(
                    () -> {
                      if (epoch != connection || !foreground) return;
                      stream = null;
                      if (e instanceof Api.Failure
                          && (((Api.Failure) e).status == 401 || ((Api.Failure) e).status == 429)) {
                        handle(e);
                        return;
                      }
                      banner.setText("Live updates paused · refreshing every 10 seconds");
                      handler.postDelayed(reconnectStream, reconnect);
                      reconnect = Math.min(reconnect * 2, 30000);
                    }),
            () ->
                handler.post(
                    () -> {
                      if (epoch == connection && foreground) {
                        reconnect = 1000;
                        banner.setText("Connected · live updates");
                        refresh();
                      }
                    }));
  }

  String display(String name) {
    JSONObject a = agent(name);
    return a == null ? name : a.optString("display", name);
  }

  JSONObject agent(String name) {
    for (int i = 0; i < agents.length(); i++)
      if (name.equals(agents.optJSONObject(i).optString("name"))) return agents.optJSONObject(i);
    return null;
  }

  String stamp(String iso) {
    try {
      return DateUtils.getRelativeTimeSpanString(
              java.time.Instant.parse(iso).toEpochMilli(),
              System.currentTimeMillis(),
              DateUtils.MINUTE_IN_MILLIS)
          .toString();
    } catch (Exception e) {
      return "No activity yet";
    }
  }

  String status(JSONObject a) {
    String s = a.optString("state", "unknown");
    if (!Arrays.asList(
            "stopped", "starting", "idle", "busy", "waiting", "crashed", "failed", "unknown")
        .contains(s)) s = "unknown";
    return s.equals("busy")
        ? "Working"
        : s.equals("unknown")
            ? "Running"
            : s.substring(0, 1).toUpperCase(Locale.ROOT) + s.substring(1);
  }

  void userRefresh() {
    if (System.currentTimeMillis() < retryAt) {
      note("Wait before refreshing again.");
      if (swipe != null) swipe.setRefreshing(false);
      return;
    }
    rateLimited = false;
    refresh();
    startStream();
  }

  void refresh() {
    if (rateLimited
        || api == null
        || loading
        || !foreground
        || System.currentTimeMillis() < retryAt) {
      if (swipe != null) swipe.setRefreshing(false);
      return;
    }
    loading = true;
    Api client = api;
    int epoch = connection, view = revision;
    String selected = peer, mode = screen;
    work.execute(
        () -> {
          try {
            JSONArray list = client.array("/api/agents"),
                convs = client.array("/api/mail/conversations");
            String user = me.isEmpty() ? client.object("/api/me").getString("name") : me;
            // The API pending bit describes only the last mail. Inspect threads for open asks.
            for (int i = 0; i < convs.length(); i++) {
              JSONObject c = convs.getJSONObject(i);
              JSONArray thread =
                  client.array(
                      "/api/mail/conversation/"
                          + Protocol.segment(c.getString("peer"))
                          + "?limit=1000");
              int pending = 0;
              for (int j = 0; j < thread.length(); j++) {
                JSONObject m = thread.getJSONObject(j);
                if (m.optString("from_name").equals(user)) pending = 0;
                else if (Protocol.kind(m).equals("ask")) pending++;
              }
              c.put("open_asks", pending);
            }
            JSONArray thread =
                selected.isEmpty()
                    ? new JSONArray()
                    : client.array(
                        "/api/mail/conversation/" + Protocol.segment(selected) + "?limit=1000");
            String capture =
                mode.equals("terminal")
                    ? client
                        .object("/api/agents/" + Protocol.segment(selected) + "/capture?lines=150")
                        .optString("capture")
                    : "";
            if (mode.equals("chat") && foreground && view == revision)
              for (int i = 0; i < thread.length(); i++) {
                JSONObject m = thread.getJSONObject(i);
                if (m.optString("to_name").equals(user) && !m.optString("status").equals("read")) {
                  client.post(
                      "/api/mail/" + Protocol.segment(m.getString("id")) + "/read",
                      new JSONObject());
                  m.put("status", "read");
                }
              }
            handler.post(
                () -> {
                  if (epoch != connection) return;
                  loading = false;
                  agents = list;
                  conversations = convs;
                  me = user;
                  if (swipe != null) swipe.setRefreshing(false);
                  if (view != revision) {
                    refresh();
                    return;
                  }
                  messages = thread;
                  terminal = capture;
                  banner.setText(
                      stream != null
                          ? "Connected · live updates"
                          : "Connected · refreshing every 10 seconds");
                  if (selected.isEmpty()) renderList();
                  else if (mode.equals("chat")) renderMessages();
                  else if (mode.equals("terminal")) renderTerminal();
                  else renderAgent();
                });
          } catch (Exception e) {
            handler.post(
                () -> {
                  if (epoch != connection) return;
                  loading = false;
                  if (swipe != null) swipe.setRefreshing(false);
                  handle(e);
                });
          }
        });
  }

  void renderList() {
    if (threadRows == null) return;
    String sig = screen + agents + conversations;
    if (sig.equals(signature)) return;
    signature = sig;
    threadRows.removeAllViews();
    boolean inbox = screen.equals("inbox");
    heading(
        threadRows,
        inbox ? "Conversations" : "Your agents",
        inbox
            ? "Questions first. Then the latest from your agents."
            : "Live state and runtime at a glance.");
    List<JSONObject> rows = new ArrayList<>();
    JSONArray source = inbox ? conversations : agents;
    for (int i = 0; i < source.length(); i++) rows.add(source.optJSONObject(i));
    rows.sort(
        (a, b) -> {
          int wait = Boolean.compare(b.optInt("open_asks") > 0, a.optInt("open_asks") > 0);
          return wait != 0
              ? wait
              : b.optString(inbox ? "last_at" : "last_activity")
                  .compareTo(a.optString(inbox ? "last_at" : "last_activity"));
        });
    if (rows.isEmpty())
      heading(
          threadRows,
          inbox ? "No conversations yet" : "No agents registered",
          inbox
              ? "Open an agent from Agents to send the first message."
              : "Register an agent on the dashboard, then pull to refresh.");
    for (JSONObject row : rows) {
      String name = row.optString(inbox ? "peer" : "name");
      JSONObject a = inbox ? agent(name) : row;
      JSONObject c = inbox ? row : conversation(name);
      LinearLayout item = column();
      item.setPadding(dp(16), dp(16), dp(16), dp(16));
      item.setBackground(surface(paper));
      LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(-1, -2);
      lp.bottomMargin = dp(12);
      threadRows.addView(item, lp);
      TextView title = text(row.optString("display", name), 20);
      title.setTypeface(null, Typeface.BOLD);
      item.addView(title);
      int asks = c == null ? 0 : c.optInt("open_asks"), unread = c == null ? 0 : c.optInt("unread");
      if (asks > 0) {
        TextView needs =
            text(
                "Waiting on you · " + asks + " open " + (asks == 1 ? "question" : "questions"), 14);
        needs.setTypeface(null, Typeface.BOLD);
        item.addView(needs);
      }
      if (unread > 0) item.addView(text(unread + " unread", 14));
      if (inbox) {
        JSONObject last = row.optJSONObject("last");
        if (last != null) {
          TextView preview = text(last.optString("body"), 16);
          preview.setMaxLines(2);
          preview.setEllipsize(TextUtils.TruncateAt.END);
          preview.setPadding(0, dp(8), 0, dp(8));
          item.addView(preview);
        }
        item.addView(quiet(stamp(row.optString("last_at"))));
      } else if (a != null) {
        JSONObject rt = a.optJSONObject("runtime");
        item.addView(text(status(a), 14));
        item.addView(
            quiet(
                rt == null
                    ? ""
                    : rt.optString("harness") + " · " + rt.optString("model", "Default model")));
        item.addView(quiet("Active " + stamp(a.optString("last_activity"))));
      }
      MaterialButton open =
          button(
              inbox ? "Open conversation" : "View agent",
              () -> open(name, inbox ? "chat" : "agent"));
      item.addView(open);
    }
  }

  JSONObject conversation(String name) {
    for (int i = 0; i < conversations.length(); i++) {
      JSONObject c = conversations.optJSONObject(i);
      if (name.equals(c.optString("peer"))) return c;
    }
    return null;
  }

  void open(String name, String mode) {
    rememberDraft();
    if (!name.equals(peer) || !mode.equals(screen)) history.add(peer + "|" + screen);
    if (!peer.equals(name)) {
      draft = "";
      messages = new JSONArray();
    }
    peer = name;
    screen = mode;
    draw();
    refresh();
  }

  void chatView() {
    actions(
        body,
        button("Agent details", () -> open(peer, "agent")),
        button("Terminal snapshot", () -> open(peer, "terminal")),
        button("Refresh", this::userRefresh));
    threadScroll = new ScrollView(this);
    threadScroll.setFillViewport(true);
    threadRows = column();
    threadScroll.addView(threadRows);
    body.addView(threadScroll, new LinearLayout.LayoutParams(-1, 0, 1));
    LinearLayout compose = new LinearLayout(this);
    compose.setGravity(Gravity.BOTTOM);
    compose.setPadding(0, dp(12), 0, dp(8));
    draftField = new EditText(this);
    draftField.setHint("Message " + display(peer));
    draftField.setContentDescription("Message to " + display(peer));
    draftField.setTextSize(16);
    draftField.setTextColor(ink);
    draftField.setHintTextColor(muted);
    draftField.setMaxLines(5);
    draftField.setMinHeight(dp(52));
    draftField.setInputType(
        android.text.InputType.TYPE_CLASS_TEXT
            | android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE
            | android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
    draftField.setBackground(surface(paper));
    draftField.setPadding(dp(16), dp(12), dp(16), dp(12));
    draftField.setText(draft);
    LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(0, -2, 1);
    p.setMarginEnd(dp(8));
    compose.addView(draftField, p);
    sendButton = primary("Send", () -> send(null, null));
    compose.addView(sendButton);
    body.addView(compose);
    draftField.addTextChangedListener(
        new TextWatcher() {
          public void beforeTextChanged(CharSequence s, int start, int count, int after) {}

          public void onTextChanged(CharSequence s, int start, int before, int count) {
            updateSend();
          }

          public void afterTextChanged(Editable e) {}
        });
    updateSend();
    renderMessages();
  }

  void updateSend() {
    if (sendButton != null)
      sendButton.setEnabled(
          !sending && draftField != null && !draftField.getText().toString().trim().isEmpty());
  }

  void renderMessages() {
    if (threadRows == null) return;
    String sig = messages.toString();
    if (sig.equals(signature)) return;
    signature = sig;
    boolean bottom =
        threadRows.getHeight() - threadScroll.getHeight() - threadScroll.getScrollY() < dp(100);
    int y = threadScroll.getScrollY();
    threadRows.removeAllViews();
    if (messages.length() == 0)
      heading(
          threadRows,
          "Start a conversation",
          "Messages, updates, deliverables and questions from " + display(peer) + " appear here.");
    for (int i = 0; i < messages.length(); i++) {
      JSONObject m = messages.optJSONObject(i);
      boolean mine = m.optString("from_name").equals(me);
      String kind = Protocol.kind(m);
      LinearLayout group = column();
      LinearLayout.LayoutParams gp = new LinearLayout.LayoutParams(-1, -2);
      gp.topMargin = dp(16);
      gp.bottomMargin = dp(4);
      if (mine) gp.setMarginStart(dp(32));
      else gp.setMarginEnd(dp(16));
      threadRows.addView(group, gp);
      group.addView(
          quiet(
              (mine ? "You" : display(peer))
                  + (kind.equals("deliver")
                      ? " · Deliverable"
                      : kind.equals("ask")
                          ? " · Question"
                          : kind.equals("decision") ? " · Decision" : "")
                  + " · "
                  + stamp(m.optString("created_at"))));
      if (kind.equals("update")) {
        TextView update = text(m.optString("body"), 14);
        update.setTextColor(muted);
        update.setTextIsSelectable(true);
        group.addView(update);
        continue;
      }
      LinearLayout card = column();
      card.setPadding(dp(16), dp(12), dp(16), dp(12));
      card.setBackground(surface(mine ? ink : paper));
      group.addView(card);
      if (!m.optString("subject").isEmpty()) {
        TextView sub = text(m.optString("subject"), kind.equals("deliver") ? 20 : 14);
        sub.setTypeface(null, Typeface.BOLD);
        sub.setTextColor(mine ? paper : ink);
        card.addView(sub);
      }
      TextView content = text("", 16);
      content.setTextColor(mine ? paper : ink);
      content.setLinkTextColor(mine ? paper : ink);
      content.setTextIsSelectable(true);
      markdown.setMarkdown(content, m.optString("body"));
      card.addView(content);
      if (kind.equals("ask") && !mine) {
        boolean answered = false;
        String choice = "";
        for (int j = i + 1; j < messages.length(); j++) {
          JSONObject later = messages.optJSONObject(j);
          if (later.optString("from_name").equals(me)) {
            answered = true;
            if (later.optString("in_reply_to").equals(m.optString("id")))
              choice = Protocol.chat(later).optString("decision");
            break;
          }
        }
        if (answered) card.addView(quiet(choice.isEmpty() ? "Answered" : "You chose " + choice));
        else {
          TextView need = text("Waiting on you", 14);
          need.setTypeface(null, Typeface.BOLD);
          need.setPadding(0, dp(12), 0, dp(4));
          card.addView(need);
          JSONArray opts = Protocol.chat(m).optJSONArray("options");
          if (opts != null)
            for (int j = 0; j < opts.length(); j++) {
              String option = opts.optString(j);
              MaterialButton choose =
                  j == 0
                      ? primary(option, () -> send(m, option))
                      : button(option, () -> send(m, option));
              choose.setEnabled(!sending);
              card.addView(choose);
            }
        }
      }
    }
    threadScroll.post(
        () -> {
          if (bottom) threadScroll.fullScroll(View.FOCUS_DOWN);
          else threadScroll.scrollTo(0, y);
        });
  }

  void send(JSONObject question, String decision) {
    if (sending || api == null || System.currentTimeMillis() < retryAt) return;
    rememberDraft();
    String value = draft.trim();
    if (decision == null && value.isEmpty()) return;
    JSONObject parent = question;
    if (parent == null)
      for (int i = messages.length() - 1; i >= 0; i--) {
        JSONObject m = messages.optJSONObject(i);
        if (m.optString("from_name").equals(peer)) {
          parent = m;
          break;
        }
      }
    final JSONObject payload;
    try {
      payload = Protocol.outgoing(peer, value, parent, decision);
    } catch (Exception e) {
      note(e.getMessage());
      return;
    }
    sending = true;
    updateSend();
    signature = "";
    renderMessages();
    Api client = api;
    int epoch = connection;
    String selected = peer;
    work.execute(
        () -> {
          try {
            client.post("/api/mail/send", payload);
            handler.post(
                () -> {
                  if (epoch != connection) return;
                  sending = false;
                  if (selected.equals(peer) && decision == null) {
                    draft = "";
                    if (draftField != null) draftField.setText("");
                  }
                  updateSend();
                  signature = "";
                  refresh();
                });
          } catch (Exception e) {
            handler.post(
                () -> {
                  if (epoch != connection) return;
                  sending = false;
                  updateSend();
                  signature = "";
                  renderMessages();
                  handle(e);
                  note(
                      "Send wasn’t confirmed. Refresh the conversation before retrying; your draft"
                          + " is saved.");
                  refresh();
                });
          }
        });
  }

  void agentView() {
    actions(
        body, primary("Messages", () -> open(peer, "chat")), button("Refresh", this::userRefresh));
    ScrollView scroll = new ScrollView(this);
    threadRows = column();
    scroll.addView(threadRows);
    body.addView(scroll, new LinearLayout.LayoutParams(-1, 0, 1));
    renderAgent();
  }

  void detail(String label, String value) {
    TextView l = quiet(label);
    l.setPadding(0, dp(20), 0, dp(4));
    threadRows.addView(l);
    TextView v = text(value, 16);
    v.setTextIsSelectable(true);
    threadRows.addView(v);
  }

  void renderAgent() {
    if (threadRows == null) return;
    JSONObject a = agent(peer);
    threadRows.removeAllViews();
    if (a == null) {
      heading(threadRows, "Agent unavailable", "Refresh to try again.");
      return;
    }
    heading(threadRows, display(peer), a.optString("role"));
    JSONObject rt = a.optJSONObject("runtime");
    detail("Status", status(a));
    detail("Harness", rt == null ? "Unknown" : rt.optString("harness"));
    detail("Model", rt == null ? "Default" : rt.optString("model", "Default"));
    detail(
        "Running since",
        a.optString("started_at").isEmpty() ? "Not running" : stamp(a.optString("started_at")));
    detail("Last activity", stamp(a.optString("last_activity")));
    detail("Directory", a.optString("dir"));
    if (a.optBoolean("pending_restart"))
      detail(
          "Restart pending",
          a.optString("pending_reason", "Configuration changed. Restart to apply."));
    if (!a.optString("detail").isEmpty()) detail("Activity", a.optString("detail"));
    if (!a.optString("last_error").isEmpty()) detail("Last error", a.optString("last_error"));
    threadRows.addView(button("View terminal snapshot", () -> open(peer, "terminal")));
    actions(
        threadRows,
        button("Wake", () -> lifecycle("start", "Wake")),
        button("Restart", () -> lifecycle("restart", "Restart")),
        button("Stop", () -> lifecycle("stop", "Stop")));
  }

  void lifecycle(String action, String label) {
    new MaterialAlertDialogBuilder(this)
        .setTitle(label + " " + display(peer) + "?")
        .setMessage(
            action.equals("stop")
                ? "This stops the agent’s running process."
                : action.equals("restart")
                    ? "The current process will restart and resume its conversation."
                    : "Start this agent’s process.")
        .setNegativeButton("Cancel", null)
        .setPositiveButton(
            label,
            (d, w) -> {
              Api client = api;
              String name = peer;
              int epoch = connection;
              work.execute(
                  () -> {
                    try {
                      client.post(
                          "/api/agents/" + Protocol.segment(name) + "/" + action, new JSONObject());
                      handler.post(
                          () -> {
                            if (epoch == connection) refresh();
                          });
                    } catch (Exception e) {
                      handler.post(
                          () -> {
                            if (epoch == connection) handle(e);
                          });
                    }
                  });
            })
        .show();
  }

  void terminalView() {
    actions(
        body,
        button("Messages", () -> open(peer, "chat")),
        button("Agent details", () -> open(peer, "agent")),
        button("Refresh snapshot", this::userRefresh));
    body.addView(quiet("Read-only snapshot · refreshes every 10 seconds while visible"));
    ScrollView scroll = new ScrollView(this);
    HorizontalScrollView horizontal = new HorizontalScrollView(this);
    threadRows = column();
    horizontal.addView(threadRows);
    scroll.addView(horizontal);
    body.addView(scroll, new LinearLayout.LayoutParams(-1, 0, 1));
    renderTerminal();
  }

  void renderTerminal() {
    if (threadRows == null) return;
    String clean = Protocol.stripAnsi(terminal);
    if (clean.equals(signature) && threadRows.getChildCount() > 0) return;
    signature = clean;
    threadRows.removeAllViews();
    TextView capture =
        text(clean.isEmpty() ? "No terminal output. The agent may be stopped." : clean, 13);
    capture.setTypeface(Typeface.MONOSPACE);
    capture.setTextIsSelectable(true);
    capture.setPadding(dp(12), dp(16), dp(12), dp(16));
    capture.setBackground(surface(paper));
    threadRows.addView(capture);
  }
}
