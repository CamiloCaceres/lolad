package dev.caceres.lolad;

import android.content.*;
import android.graphics.Typeface;
import android.net.Uri;
import android.os.*;
import android.provider.DocumentsContract;
import android.text.*;
import android.util.Log;
import android.view.*;
import android.view.inputmethod.InputMethodManager;
import android.widget.*;
import java.io.IOException;
import java.io.OutputStream;
import androidx.activity.ComponentActivity;
import androidx.activity.OnBackPressedCallback;
import androidx.activity.result.ActivityResultLauncher;
import androidx.activity.result.contract.ActivityResultContracts;
import androidx.core.graphics.Insets;
import androidx.core.view.*;
import androidx.recyclerview.widget.LinearLayoutManager;
import androidx.recyclerview.widget.RecyclerView;
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout;
import com.google.android.material.button.MaterialButton;
import com.google.android.material.dialog.MaterialAlertDialogBuilder;
import com.google.android.material.snackbar.Snackbar;
import com.google.android.material.textfield.*;
import com.journeyapps.barcodescanner.*;
import java.util.*;
import java.util.concurrent.*;
import okhttp3.Call;
import org.json.*;

/**
 * Screens own layout; data is fetched and parsed on a single IO executor and
 * applied as immutable rows. The live stream is coalesced into at most one
 * refresh per 150 ms window and only touches what its event names.
 */
public final class MainActivity extends ComponentActivity {
  private static final String START_TAG = "lolad.start";
  private static final String PERF_TAG = "lolad.perf";

  private static final int AGENTS = 1;
  private static final int CONVERSATIONS = 2;
  private static final int CHAT = 4;
  private static final int AGENT = 8;
  private static final int TERMINAL = 16;
  private static final int ALL = AGENTS | CONVERSATIONS | CHAT | AGENT | TERMINAL;

  private static final int THEME_SYSTEM = 101, THEME_LIGHT = 102, THEME_DARK = 103;
  private static final int MENU_DISCONNECT = 200;

  final Handler handler = new Handler(Looper.getMainLooper());
  ExecutorService io;
  Session session;
  Api api;
  LocalCache cache;
  Ui ui;
  MarkdownCache markdown;
  Call stream;
  ThreadAdapter threadAdapter;
  ConversationAdapter conversationAdapter;
  AgentAdapter agentAdapter;
  Uploads uploads;
  ImageCache images;

  LinearLayout root, bar;
  FrameLayout body;
  TextView titleView, banner;

  LinearLayout listScreen, chatScreen, agentScreen, terminalScreen, connectScreen;
  TextView listHeading, listDetail, emptyTitle, emptyDetail;
  LinearLayout emptyBox, navTrack;
  MaterialButton segmentInbox, segmentAgents, refreshButton, backButton, moreButton, attachButton;
  SwipeRefreshLayout inboxSwipe, agentsSwipe, activeSwipe;
  RecyclerView inboxList, agentsList, chatList;
  EditText draftField;
  MaterialButton sendButton;
  LinearLayout composerBox, chipsRow;
  HorizontalScrollView tray;
  TextView trayNote, trayLabel, chatEmpty;
  ScrollView agentScroll, terminalScroll;
  HorizontalScrollView terminalHorizontal;
  LinearLayout agentColumn;
  TextView terminalView;
  TextInputLayout urlWrap, codeWrap, tokenWrap;
  EditText urlField, codeField, tokenField;

  volatile JSONArray agentsJson = new JSONArray();
  volatile JSONArray conversationsJson = new JSONArray();
  volatile String me = "";
  volatile String peer = "";
  volatile String screen = "inbox";
  volatile String draft = "";
  volatile boolean foreground, sending, busy, rateLimited, booted;
  volatile int connection;
  String savedServer = "";
  String pendingLink;
  final ArrayList<String> history = new ArrayList<>();
  final Map<String, List<Model.Message>> threadCache = new HashMap<>();
  final Map<String, JSONArray> threadJsonCache = new HashMap<>();
  final Map<String, Integer> askMap = new ConcurrentHashMap<>();
  final Map<String, AskState> askCache = new ConcurrentHashMap<>();
  final Set<String> readRequested = new HashSet<>();

  String chatPeer;
  String terminalText = "", terminalSignature = "";
  JSONObject agentDetail;
  String agentSignature = "";
  long threadOpenStart;
  boolean cacheLogged, networkLogged, forceBottom;
  int themeMode;
  boolean remoteBlocked, draftFocused, focusApplied, trayFlashActive;
  int pendingEchoSeq;
  Model.Message pendingEcho;
  Attachment downloadTarget;
  androidx.appcompat.app.AlertDialog downloadDialog;
  okhttp3.Call downloadCall;
  ActivityResultLauncher<String[]> pickFiles;
  ActivityResultLauncher<String> createDocument;
  int restoreChatPosition = -1, restoreChatOffset;
  String restoreChatPeer;
  int restoreAgentScroll = -1, restoreTerminalScroll = -1;
  int[] savedListPositions = {-1, -1};
  final Uploads.Watcher uploadWatcher = this::uploadsChanged;

  int pendingScope, sseScope;
  boolean fetching, sseScheduled;
  long retryAt, lastScrollAt;
  int reconnect = 1000;
  final long startedAt = SystemClock.uptimeMillis();

  final Runnable poll =
      new Runnable() {
        public void run() {
          if (foreground && api != null) {
            if (SystemClock.uptimeMillis() - lastScrollAt < 2500) {
              handler.postDelayed(this, 2000);
              return;
            }
            refresh(scopesFor(screen));
            handler.postDelayed(this, 10000);
          }
        }
      };
  final Runnable reconnectStream = () -> startStream();
  final Runnable sseFlush =
      () -> {
        sseScheduled = false;
        int scope = sseScope;
        sseScope = 0;
        if (scope != 0) refresh(scope);
      };

  /** An explicit Light/Dark choice is applied before any resource is resolved. */
  @Override
  protected void attachBaseContext(Context base) {
    super.attachBaseContext(ThemeMode.wrap(base, ThemeMode.read(base)));
  }

  @Override
  public void onCreate(Bundle saved) {
    if (BuildConfig.DEBUG) {
      StrictMode.setThreadPolicy(
          new StrictMode.ThreadPolicy.Builder().detectAll().penaltyLog().build());
      // Leak checks only: untagged sockets are a tagging suggestion, not
      // main-thread work, and the task gate is disk/network on the UI thread.
      StrictMode.setVmPolicy(
          new StrictMode.VmPolicy.Builder()
              .detectActivityLeaks()
              .detectLeakedClosableObjects()
              .detectLeakedRegistrationObjects()
              .detectLeakedSqlLiteObjects()
              .penaltyLog()
              .build());
    }
    super.onCreate(saved);
    themeMode = ThemeMode.read(this);
    ui = new Ui(this);
    io = Executors.newSingleThreadExecutor();
    uploads = Uploads.shared(this);
    images = ImageCache.shared(this);
    uploads.setWatcher(uploadWatcher);
    conversationAdapter = new ConversationAdapter(ui, this::openConversation);
    agentAdapter = new AgentAdapter(ui, this::openAgent);

    boolean dark = ThemeMode.isDark(this);
    WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
    WindowCompat.getInsetsController(getWindow(), getWindow().getDecorView())
        .setAppearanceLightStatusBars(!dark);
    WindowCompat.getInsetsController(getWindow(), getWindow().getDecorView())
        .setAppearanceLightNavigationBars(!dark);

    if (saved != null) {
      ArrayList<String> prior = saved.getStringArrayList("history");
      if (prior != null) history.addAll(prior);
      screen = saved.getString("screen", "inbox");
      peer = saved.getString("peer", "");
      draft = saved.getString("draft", "");
      if (peer.isEmpty()
          && !screen.equals("inbox")
          && !screen.equals("agents")
          && !screen.equals("connect")) screen = "inbox";
      draftFocused = saved.getBoolean("draft_focused", false);
      restoreChatPosition = saved.getInt("chat_pos", -1);
      restoreChatOffset = saved.getInt("chat_offset", 0);
      restoreChatPeer = saved.getString("chat_peer");
      restoreAgentScroll = saved.getInt("agent_scroll", -1);
      restoreTerminalScroll = saved.getInt("terminal_scroll", -1);
      savedListPositions[0] = saved.getInt("inbox_pos", -1);
      savedListPositions[1] = saved.getInt("agents_pos", -1);
    }

    root = new LinearLayout(this);
    root.setOrientation(LinearLayout.VERTICAL);
    root.setBackgroundColor(ui.backdrop);
    root.setPadding(ui.dp(16), 0, ui.dp(16), 0);
    setContentView(root);
    ViewCompat.setOnApplyWindowInsetsListener(
        root,
        (v, insets) -> {
          Insets bars =
              insets.getInsets(
                  WindowInsetsCompat.Type.systemBars() | WindowInsetsCompat.Type.displayCutout());
          int bottom =
              Math.max(bars.bottom, insets.getInsets(WindowInsetsCompat.Type.ime()).bottom);
          root.setPadding(bars.left + ui.dp(16), bars.top, bars.right + ui.dp(16), bottom);
          return insets;
        });

    buildChrome();
    getOnBackPressedDispatcher()
        .addCallback(
            this,
            new OnBackPressedCallback(true) {
              public void handleOnBackPressed() {
                onBack();
              }
            });
    scanner =
        registerForActivityResult(
            new ScanContract(),
            result -> {
              if (result.getContents() != null) pairLink(result.getContents());
            });
    pickFiles =
        registerForActivityResult(
            new ActivityResultContracts.OpenMultipleDocuments(),
            uris -> {
              if (uris == null) return;
              for (Uri uri : uris) addAttachmentUri(uri, false);
            });
    createDocument =
        registerForActivityResult(
            new ActivityResultContracts.CreateDocument("*/*"),
            uri -> {
              Attachment target = downloadTarget;
              downloadTarget = null;
              if (uri != null && target != null) beginDownload(target, uri);
            });
    if (getIntent().getData() != null) pendingLink = getIntent().getData().toString();
    banner.setText("Starting…");
    boot();
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
    out.putStringArrayList("history", new ArrayList<>(history));
    out.putString("screen", screen);
    out.putString("peer", peer);
    out.putString("draft", draft);
    out.putBoolean("draft_focused", draftField != null && draftField.hasFocus());
    if (chatList != null
        && threadAdapter != null
        && threadAdapter.getItemCount() > 0
        && screen.equals("chat")) {
      out.putString("chat_peer", peer);
      LinearLayoutManager manager = (LinearLayoutManager) chatList.getLayoutManager();
      int position = manager.findFirstVisibleItemPosition();
      View anchor = position < 0 ? null : manager.findViewByPosition(position);
      out.putInt("chat_pos", position);
      out.putInt(
          "chat_offset",
          anchor == null ? 0 : anchor.getTop() - chatList.getPaddingTop());
    }
    out.putInt("inbox_pos", firstVisible(inboxList));
    out.putInt("agents_pos", firstVisible(agentsList));
    if (agentScroll != null) out.putInt("agent_scroll", agentScroll.getScrollY());
    if (terminalScroll != null) out.putInt("terminal_scroll", terminalScroll.getScrollY());
    super.onSaveInstanceState(out);
  }

  @Override
  protected void onStart() {
    super.onStart();
    foreground = true;
    if (api != null) {
      handler.postDelayed(poll, 10000);
      startStream();
      refresh(scopesFor(screen));
    }
  }

  @Override
  protected void onStop() {
    foreground = false;
    rememberDraft();
    handler.removeCallbacks(poll);
    handler.removeCallbacks(reconnectStream);
    handler.removeCallbacks(sseFlush);
    sseScheduled = false;
    if (stream != null) {
      stream.cancel();
      stream = null;
    }
    super.onStop();
  }

  @Override
  public void onTrimMemory(int level) {
    super.onTrimMemory(level);
    if (level >= TRIM_MEMORY_RUNNING_LOW && images != null) images.trimMemory();
  }

  @Override
  protected void onDestroy() {
    handler.removeCallbacksAndMessages(null);
    if (downloadCall != null) {
      downloadCall.cancel();
      downloadCall = null;
    }
    if (downloadDialog != null) {
      downloadDialog.dismiss();
      downloadDialog = null;
    }
    uploads.clearWatcher(uploadWatcher);
    io.shutdownNow();
    if (markdown != null) markdown.shutdown();
    super.onDestroy();
  }

  private void post(Runnable runnable) {
    handler.post(
        () -> {
          if (!isDestroyed() && !isFinishing()) runnable.run();
        });
  }

  private void boot() {
    io.execute(
        () -> {
          Session loaded = null;
          String server = "", token = "";
          try {
            loaded = new Session(new KeystoreStorage(this));
            server = loaded.server();
            token = loaded.token();
          } catch (Exception ignored) {
            // Treat an unreadable credential store as disconnected.
          }
          if (cache == null) cache = new LocalCache(this);
          boolean connected = !server.isEmpty() && !token.isEmpty();
          LocalCache.Snapshot snapshot = connected ? cache.read() : null;
          List<Model.Conversation> conversationRows = null;
          List<Model.Agent> agentRows = null;
          if (snapshot != null) {
            synchronized (askMap) {
              askMap.clear();
              askMap.putAll(snapshot.asks);
            }
            conversationRows = Model.conversations(snapshot.conversations, snapshot.asks);
            agentRows =
                Model.agents(snapshot.agents, snapshot.asks, unreadByPeer(snapshot.conversations));
          }
          final Session loadedSession = loaded;
          final String finalServer = server, finalToken = token;
          final boolean isConnected = connected;
          final LocalCache.Snapshot finalSnapshot = snapshot;
          final List<Model.Conversation> finalConversations = conversationRows;
          final List<Model.Agent> finalAgents = agentRows;
          post(
              () -> {
                session = loadedSession;
                savedServer = finalServer;
                if (!isConnected) {
                  showConnect("");
                  applyPendingLink();
                  return;
                }
                api = new Api(finalServer, finalToken);
                if (finalSnapshot != null) {
                  agentsJson = finalSnapshot.agents;
                  conversationsJson = finalSnapshot.conversations;
                  conversationAdapter.submitList(finalConversations);
                  agentAdapter.submitList(finalAgents);
                  banner.setText("Saved state · refreshing…");
                  if (!cacheLogged) {
                    cacheLogged = true;
                    Log.i(START_TAG, "cached-screen-ms=" + (SystemClock.uptimeMillis() - startedAt));
                  }
                }
                showCurrent();
                if (finalSnapshot != null) logNextDraw("cached-first-frame-ms");
                applyPendingLink();
                startRealtime();
                refresh(ALL);
              });
        });
  }

  private void applyPendingLink() {
    if (pendingLink == null) return;
    String link = pendingLink;
    pendingLink = null;
    pairLink(link);
  }

  private void buildChrome() {
    bar = new LinearLayout(this);
    bar.setOrientation(LinearLayout.HORIZONTAL);
    bar.setGravity(Gravity.CENTER_VERTICAL);
    bar.setMinimumHeight(ui.dp(56));
    backButton = ui.textAction("Back", this::onBack);
    backButton.setVisibility(View.GONE);
    bar.addView(backButton);
    titleView = ui.organization("lolad");
    titleView.setMaxLines(1);
    titleView.setEllipsize(TextUtils.TruncateAt.END);
    bar.addView(titleView, new LinearLayout.LayoutParams(0, -2, 1));
    moreButton = ui.icon(R.drawable.ic_more, "More actions", () -> showOverflow(moreButton));
    bar.addView(moreButton);
    root.addView(bar);
    root.addView(ui.hairline());
    banner = ui.quiet("Starting…");
    banner.setPadding(0, ui.dp(5), 0, ui.dp(8));
    banner.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
    root.addView(banner);
    body = new FrameLayout(this);
    root.addView(body, new LinearLayout.LayoutParams(-1, 0, 1));
  }

  private void renderChrome() {
    boolean list = peer.isEmpty();
    titleView.setText(list ? "lolad" : display(peer));
    titleView.setTextSize(
        android.util.TypedValue.COMPLEX_UNIT_SP, list ? Ui.ORGANIZATION : Ui.TITLE);
    titleView.setLetterSpacing(list ? -0.022f : -0.017f);
    titleView.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
    backButton.setVisibility(list ? View.GONE : View.VISIBLE);
    moreButton.setVisibility(View.VISIBLE);
  }

  private void showOverflow(View anchor) {
    PopupMenu menu = new PopupMenu(this, anchor);
    SubMenu theme = menu.getMenu().addSubMenu("Theme");
    theme.setGroupCheckable(0, true, true);
    theme.add(0, THEME_SYSTEM, 0, "Follow system").setCheckable(true).setChecked(themeMode == ThemeMode.SYSTEM);
    theme.add(0, THEME_LIGHT, 1, "Light").setCheckable(true).setChecked(themeMode == ThemeMode.LIGHT);
    theme.add(0, THEME_DARK, 2, "Dark").setCheckable(true).setChecked(themeMode == ThemeMode.DARK);
    if (api != null) menu.getMenu().add(0, MENU_DISCONNECT, 1, "Disconnect");
    menu.setOnMenuItemClickListener(
        item -> {
          switch (item.getItemId()) {
            case THEME_SYSTEM:
              applyTheme(ThemeMode.SYSTEM);
              return true;
            case THEME_LIGHT:
              applyTheme(ThemeMode.LIGHT);
              return true;
            case THEME_DARK:
              applyTheme(ThemeMode.DARK);
              return true;
            case MENU_DISCONNECT:
              confirmDisconnect();
              return true;
            default:
              return false;
          }
        });
    menu.show();
  }

  /** Recreates with the saved instance state: screen, draft, scroll and uploads survive. */
  private void applyTheme(int mode) {
    if (mode == themeMode) return;
    themeMode = mode;
    ThemeMode.write(this, mode);
    rememberDraft();
    recreate();
  }

  private int firstVisible(RecyclerView list) {
    if (list == null) return -1;
    RecyclerView.LayoutManager manager = list.getLayoutManager();
    return manager instanceof LinearLayoutManager
        ? ((LinearLayoutManager) manager).findFirstVisibleItemPosition()
        : -1;
  }

  private void restoreListPositions() {
    if (inboxList != null
        && savedListPositions[0] >= 0
        && savedListPositions[0] <= conversationAdapter.getItemCount()) {
      ((LinearLayoutManager) inboxList.getLayoutManager())
          .scrollToPositionWithOffset(savedListPositions[0], 0);
      savedListPositions[0] = -1;
    }
    if (agentsList != null
        && savedListPositions[1] >= 0
        && savedListPositions[1] <= agentAdapter.getItemCount()) {
      ((LinearLayoutManager) agentsList.getLayoutManager())
          .scrollToPositionWithOffset(savedListPositions[1], 0);
      savedListPositions[1] = -1;
    }
  }

  private void showCurrent() {
    renderChrome();
    if (api == null) {
      showConnect("");
      return;
    }
    switch (screen) {
      case "chat":
        showChat();
        break;
      case "agent":
        showAgent();
        break;
      case "terminal":
        showTerminal();
        break;
      case "agents":
        showList();
        break;
      default:
        showList();
    }
  }

  private void swapBody(View view) {
    if (body.getChildCount() == 1 && body.getChildAt(0) == view) return;
    body.removeAllViews();
    body.addView(
        view,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
  }

  private void showList() {
    if (!screen.equals("agents")) screen = "inbox";
    ensureListScreen();
    renderChrome();
    boolean inbox = screen.equals("inbox");
    listHeading.setText(inbox ? "Conversations" : "Your agents");
    listDetail.setText(
        inbox
            ? "Questions first. Then the latest from your agents."
            : "Live state and runtime at a glance.");
    inboxSwipe.setVisibility(inbox ? View.VISIBLE : View.GONE);
    agentsSwipe.setVisibility(inbox ? View.GONE : View.VISIBLE);
    activeSwipe = inbox ? inboxSwipe : agentsSwipe;
    ui.selectSegment(segmentInbox, inbox);
    ui.selectSegment(segmentAgents, !inbox);
    inboxList.setAdapter(conversationAdapter);
    agentsList.setAdapter(agentAdapter);
    swapBody(listScreen);
    updateEmpty();
  }

  private void ensureListScreen() {
    if (listScreen != null) return;
    listScreen = new LinearLayout(this);
    listScreen.setOrientation(LinearLayout.VERTICAL);

    listHeading = ui.headline("");
    listScreen.addView(listHeading);
    listDetail = ui.text("", Ui.LABEL, ui.muted, 400, 2);
    listDetail.setPadding(0, ui.dp(5), 0, ui.dp(12));
    listScreen.addView(listDetail);

    LinearLayout nav = new LinearLayout(this);
    nav.setOrientation(LinearLayout.HORIZONTAL);
    nav.setGravity(Gravity.CENTER_VERTICAL);
    nav.setPadding(0, 0, 0, ui.dp(12));
    navTrack = ui.segmentTrack();
    segmentInbox = ui.segment("Inbox", () -> setListMode("inbox"));
    segmentAgents = ui.segment("Agents", () -> setListMode("agents"));
    navTrack.addView(
        segmentInbox, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
    navTrack.addView(
        segmentAgents, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
    nav.addView(navTrack, new LinearLayout.LayoutParams(0, -2, 1f));
    refreshButton = ui.quietAction("Refresh", this::userRefresh);
    LinearLayout.LayoutParams refreshParams =
        new LinearLayout.LayoutParams(-2, LinearLayout.LayoutParams.WRAP_CONTENT);
    refreshParams.setMarginStart(ui.dp(8));
    nav.addView(refreshButton, refreshParams);
    listScreen.addView(nav);

    FrameLayout pane = new FrameLayout(this);
    listScreen.addView(pane, new LinearLayout.LayoutParams(-1, 0, 1f));

    inboxList = list(conversationAdapter);
    inboxList.setId(R.id.list_inbox);
    agentsList = list(agentAdapter);
    agentsList.setId(R.id.list_agents);
    inboxSwipe = swipe(inboxList);
    agentsSwipe = swipe(agentsList);
    pane.addView(
        inboxSwipe,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
    pane.addView(
        agentsSwipe,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

    emptyBox = new LinearLayout(this);
    emptyBox.setOrientation(LinearLayout.VERTICAL);
    emptyBox.setGravity(Gravity.CENTER_HORIZONTAL);
    emptyBox.setPadding(ui.dp(24), ui.dp(24), ui.dp(24), ui.dp(24));
    emptyBox.setVisibility(View.GONE);
    emptyTitle = ui.rowTitle("");
    emptyTitle.setGravity(Gravity.CENTER);
    emptyBox.addView(emptyTitle);
    emptyDetail = ui.quiet("");
    emptyDetail.setGravity(Gravity.CENTER);
    emptyDetail.setPadding(0, ui.dp(6), 0, 0);
    emptyBox.addView(emptyDetail);
    pane.addView(
        emptyBox,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.WRAP_CONTENT,
            FrameLayout.LayoutParams.WRAP_CONTENT,
            Gravity.CENTER));
  }

  private RecyclerView list(RecyclerView.Adapter<?> adapter) {
    RecyclerView view = new RecyclerView(this);
    view.setLayoutManager(new LinearLayoutManager(this));
    view.setAdapter(adapter);
    view.setItemAnimator(null);
    view.setHasFixedSize(true);
    view.setItemViewCacheSize(12);
    view.setClipToPadding(false);
    view.setPadding(0, 0, 0, ui.dp(16));
    return view;
  }

  private SwipeRefreshLayout swipe(RecyclerView list) {
    SwipeRefreshLayout refresh = new SwipeRefreshLayout(this);
    refresh.addView(
        list,
        new ViewGroup.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    refresh.setColorSchemeColors(ui.ink, ui.secondary);
    refresh.setProgressBackgroundColorSchemeColor(ui.paper);
    refresh.setOnRefreshListener(this::userRefresh);
    return refresh;
  }

  private void setListMode(String mode) {
    if (mode.equals(screen)) return;
    screen = mode;
    peer = "";
    showList();
    if (api != null) refresh(AGENTS | CONVERSATIONS);
  }

  private void updateEmpty() {
    if (emptyBox == null) return;
    boolean inbox = screen.equals("inbox");
    int count = inbox ? conversationAdapter.getItemCount() : agentAdapter.getItemCount();
    emptyTitle.setText(inbox ? "No conversations yet" : "No agents registered");
    emptyDetail.setText(
        inbox
            ? "Open an agent from Agents to send the first message."
            : "Register an agent on the dashboard, then pull to refresh.");
    emptyBox.setVisibility(count == 0 ? View.VISIBLE : View.GONE);
  }

  private void showChat() {
    if (peer.isEmpty()) {
      showList();
      return;
    }
    ensureChatScreen();
    renderChrome();
    swapBody(chatScreen);
    threadAdapter.setContext(api, peer, images);
    if (restoreChatPeer != null && !restoreChatPeer.equals(peer)) restoreChatPosition = -1;
    boolean newPeer = !peer.equals(chatPeer);
    chatPeer = peer;
    if (newPeer) {
      threadOpenStart = SystemClock.uptimeMillis();
      threadAdapter.submitList(new ArrayList<>());
      draftField.setText(draft);
      List<Model.Message> cached = threadCache.get(peer);
      if (cached != null) submitThread(peer, cached);
      else banner.setText("Loading conversation…");
    } else if (!draftField.getText().toString().equals(draft)) {
      draftField.setText(draft);
    }
    draftField.setHint("Message " + display(peer));
    draftField.setContentDescription("Message to " + display(peer));
    renderTray();
    updateSend();
    if (draftFocused && !focusApplied) {
      focusApplied = true;
      draftFocused = false;
      draftField.post(
          () -> {
            draftField.requestFocus();
            InputMethodManager keyboard =
                (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
            if (keyboard != null) keyboard.showSoftInput(draftField, InputMethodManager.SHOW_IMPLICIT);
          });
    }
    if (api != null) refresh(CHAT | AGENTS);
  }

  private void ensureChatScreen() {
    if (chatScreen != null) return;
    if (markdown == null) markdown = new MarkdownCache(this);
    threadAdapter = new ThreadAdapter(ui, markdown, this::decide, this::openAttachment);
    threadAdapter.setOnFlushed(
        () -> {
          if (chatList != null
              && !chatList.canScrollVertically(1)
              && threadAdapter.getItemCount() > 0)
            chatList.scrollToPosition(threadAdapter.getItemCount() - 1);
        });

    chatScreen = new LinearLayout(this);
    chatScreen.setOrientation(LinearLayout.VERTICAL);
    actions(
        chatScreen,
        ui.quietAction("Agent details", () -> open(peer, "agent")),
        ui.quietAction("Terminal snapshot", () -> open(peer, "terminal")),
        ui.quietAction("Refresh", this::userRefresh));

    FrameLayout pane = new FrameLayout(this);
    chatList = new RecyclerView(this);
    chatList.setId(R.id.list_chat);
    chatList.setLayoutManager(new LinearLayoutManager(this));
    chatList.setAdapter(threadAdapter);
    chatList.setItemAnimator(null);
    chatList.setHasFixedSize(true);
    chatList.setItemViewCacheSize(24);
    chatList.setClipToPadding(false);
    chatList.setPadding(0, ui.dp(4), 0, ui.dp(12));
    chatList.addOnScrollListener(
        new RecyclerView.OnScrollListener() {
          @Override
          public void onScrollStateChanged(RecyclerView recyclerView, int newState) {
            lastScrollAt = SystemClock.uptimeMillis();
            if (threadAdapter != null)
              threadAdapter.setScrolling(newState != RecyclerView.SCROLL_STATE_IDLE);
            if (newState == RecyclerView.SCROLL_STATE_IDLE) markVisibleRead();
          }
        });
    pane.addView(
        chatList,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
    chatEmpty = ui.quiet("No messages yet. Say hello or attach a file.");
    chatEmpty.setGravity(Gravity.CENTER);
    chatEmpty.setPadding(ui.dp(24), 0, ui.dp(24), 0);
    chatEmpty.setVisibility(View.GONE);
    pane.addView(
        chatEmpty,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.WRAP_CONTENT,
            FrameLayout.LayoutParams.WRAP_CONTENT,
            Gravity.CENTER));
    chatScreen.addView(pane, new LinearLayout.LayoutParams(-1, 0, 1f));

    composerBox = new LinearLayout(this);
    composerBox.setOrientation(LinearLayout.VERTICAL);
    composerBox.setPadding(0, ui.dp(6), 0, ui.dp(4));
    composerBox.setBackgroundColor(ui.backdrop);

    trayLabel = ui.quiet("");
    trayLabel.setVisibility(View.GONE);
    trayLabel.setPadding(0, 0, 0, ui.dp(4));
    composerBox.addView(trayLabel);

    tray = new HorizontalScrollView(this);
    tray.setHorizontalScrollBarEnabled(false);
    tray.setClipToPadding(false);
    chipsRow = new LinearLayout(this);
    chipsRow.setOrientation(LinearLayout.HORIZONTAL);
    tray.addView(chipsRow);
    tray.setVisibility(View.GONE);
    composerBox.addView(tray);

    trayNote = ui.text("", Ui.LABEL, ui.attention, 600, 1);
    trayNote.setVisibility(View.GONE);
    trayNote.setPadding(0, ui.dp(6), 0, 0);
    trayNote.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
    composerBox.addView(trayNote);

    LinearLayout composer = new LinearLayout(this);
    composer.setOrientation(LinearLayout.HORIZONTAL);
    composer.setGravity(Gravity.BOTTOM);
    composer.setPadding(0, ui.dp(8), 0, 0);
    attachButton = ui.icon(R.drawable.ic_attach, "Attach files", this::pickAttachments);
    LinearLayout.LayoutParams attachParams =
        new LinearLayout.LayoutParams(ui.dp(48), ui.dp(52));
    attachParams.setMarginEnd(ui.dp(4));
    composer.addView(attachButton, attachParams);

    draftField = new EditText(this);
    draftField.setId(R.id.draft_field);
    draftField.setTextSize(android.util.TypedValue.COMPLEX_UNIT_SP, Ui.BODY);
    draftField.setTextColor(ui.ink);
    draftField.setHintTextColor(ui.muted);
    draftField.setMaxLines(5);
    draftField.setMinHeight(ui.dp(52));
    draftField.setInputType(
        android.text.InputType.TYPE_CLASS_TEXT
            | android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE
            | android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
    draftField.setBackground(ui.outlined(ui.raised, Ui.FIELD_RADIUS, ui.hairline));
    draftField.setPadding(ui.dp(16), ui.dp(12), ui.dp(16), ui.dp(12));
    draftField.setImeOptions(android.view.inputmethod.EditorInfo.IME_ACTION_SEND);
    draftField.setOnEditorActionListener(
        (v, actionId, event) -> {
          if (actionId == android.view.inputmethod.EditorInfo.IME_ACTION_SEND) {
            send(null, null);
            return true;
          }
          return false;
        });
    LinearLayout.LayoutParams fieldParams = new LinearLayout.LayoutParams(0, -2, 1f);
    fieldParams.setMarginEnd(ui.dp(8));
    composer.addView(draftField, fieldParams);
    sendButton = ui.primary("Send", () -> send(null, null));
    sendButton.setMinHeight(ui.dp(52));
    composer.addView(sendButton);
    composerBox.addView(composer);
    chatScreen.addView(composerBox);
    draftField.addTextChangedListener(
        new TextWatcher() {
          public void beforeTextChanged(CharSequence s, int start, int count, int after) {}

          public void onTextChanged(CharSequence s, int start, int before, int count) {
            updateSend();
          }

          public void afterTextChanged(Editable e) {}
        });
    installPasteAndDrop();
    draftField.setText(draft);
    updateSend();
  }

  /** Pasted text keeps its native behaviour; pasted files and images become attachments. */
  private void installPasteAndDrop() {
    ViewCompat.setOnReceiveContentListener(
        draftField,
        new String[] {"*/*"},
        (view, payload) -> {
          ClipData clip = payload.getClip();
          if (clip == null || clip.getItemCount() == 0) return payload;
          if (hasText(clip)) return payload;
          List<Uri> uris = clipUris(clip);
          if (uris.isEmpty()) return payload;
          if (!canAttach()) {
            trayMessage(remoteReason());
            return null;
          }
          String mime = clip.getDescription().getMimeType(0);
          boolean imageData = mime != null && mime.startsWith("image/") && !"text/uri-list".equals(mime);
          for (Uri uri : uris) addAttachmentUri(uri, imageData);
          return null;
        });
    View.OnDragListener drops = this::handleDrag;
    composerBox.setOnDragListener(drops);
    chatList.setOnDragListener(drops);
    // The field itself normally receives content drags; route file drops through
    // the same handler so the hover highlight is visible over the composer too.
    draftField.setOnDragListener(
        (view, event) -> {
          if (event.getAction() == DragEvent.ACTION_DRAG_STARTED) {
            ClipDescription description = event.getClipDescription();
            if (description == null) return false;
            for (int i = 0; i < description.getMimeTypeCount(); i++) {
              String mime = description.getMimeType(i);
              if (!"text/plain".equals(mime) && (mime.startsWith("image/") || mime.startsWith("application/") || "text/uri-list".equals(mime)))
                return true;
            }
            return false;
          }
          return handleDrag(view, event);
        });
  }

  private boolean hasText(ClipData clip) {
    for (int i = 0; i < clip.getItemCount(); i++) {
      if (clip.getItemAt(i).getText() != null) return true;
    }
    ClipDescription description = clip.getDescription();
    for (int i = 0; i < description.getMimeTypeCount(); i++) {
      if ("text/plain".equals(description.getMimeType(i))) return true;
    }
    return false;
  }

  private List<Uri> clipUris(ClipData clip) {
    List<Uri> uris = new ArrayList<>();
    for (int i = 0; i < clip.getItemCount(); i++) {
      Uri uri = clip.getItemAt(i).getUri();
      if (uri != null && !uris.contains(uri)) uris.add(uri);
    }
    return uris;
  }

  private boolean handleDrag(View view, DragEvent event) {
    switch (event.getAction()) {
      case DragEvent.ACTION_DRAG_STARTED:
        ClipDescription description = event.getClipDescription();
        if (description == null) return false;
        for (int i = 0; i < description.getMimeTypeCount(); i++) {
          String mime = description.getMimeType(i);
          if ("text/uri-list".equals(mime) || mime.startsWith("image/") || mime.startsWith("application/")) {
            return true;
          }
        }
        return false;
      case DragEvent.ACTION_DRAG_ENTERED:
        if (composerBox != null && canAttach())
          composerBox.setBackground(ui.outlined(ui.quiet, 14, ui.awake));
        return true;
      case DragEvent.ACTION_DRAG_EXITED:
      case DragEvent.ACTION_DRAG_ENDED:
        if (composerBox != null) composerBox.setBackgroundColor(ui.backdrop);
        return true;
      case DragEvent.ACTION_DROP:
        if (composerBox != null) composerBox.setBackgroundColor(ui.backdrop);
        if (!canAttach()) {
          trayMessage(remoteReason());
          return true;
        }
        ClipData clip = event.getClipData();
        if (clip != null) for (Uri uri : clipUris(clip)) addAttachmentUri(uri, false);
        return true;
      default:
        return false;
    }
  }

  private boolean canAttach() {
    return api != null && !peer.isEmpty() && screen.equals("chat") && !Attachments.isRemotePeer(peer);
  }

  private String remoteReason() {
    return "Attachments aren’t available for remote agents.";
  }

  private void pickAttachments() {
    if (!canAttach()) {
      trayMessage(remoteReason());
      return;
    }
    if (uploads.count(peer) >= Attachments.MAX_FILES) {
      trayMessage(limitMessage());
      return;
    }
    try {
      pickFiles.launch(new String[] {"*/*"});
    } catch (Exception e) {
      note("No file picker is available on this device.");
    }
  }

  private String limitMessage() {
    return "Up to " + Attachments.MAX_FILES + " files per message. Remove one to add another.";
  }

  private void addAttachmentUri(Uri uri, boolean pastedImage) {
    if (uri == null || !canAttach()) {
      if (uri != null) trayMessage(remoteReason());
      return;
    }
    int result = uploads.add(api, peer, uri, pastedImage, System.currentTimeMillis());
    switch (result) {
      case Uploads.LIMIT:
        trayMessage(limitMessage());
        break;
      case Uploads.TOO_LARGE:
        trayMessage("That file is larger than the 500 MB limit.");
        break;
      case Uploads.UNAVAILABLE:
        trayMessage("That file can’t be read. Try another one.");
        break;
      default:
        trayFlashActive = false;
        renderTray();
        break;
    }
    updateSend();
  }

  private void uploadsChanged(String changedPeer) {
    if (!changedPeer.equals(peer) || !"chat".equals(screen)) return;
    for (Uploads.Upload upload : uploads.list(changedPeer)) {
      if (upload.failure != null && upload.failure.status == 401) {
        handle(upload.failure);
        return;
      }
    }
    renderTray();
    updateSend();
  }

  private void renderTray() {
    if (chipsRow == null || tray == null) return;
    List<Uploads.Upload> items = peer.isEmpty() ? new ArrayList<>() : uploads.list(peer);
    chipsRow.removeAllViews();
    int visible = items.isEmpty() ? View.GONE : View.VISIBLE;
    tray.setVisibility(visible);
    trayLabel.setVisibility(visible);
    if (items.isEmpty()) {
      trayLabel.setText("");
    } else {
      trayLabel.setText("Files · " + items.size() + " of " + Attachments.MAX_FILES);
      int width = Math.min(ui.dp(250), getResources().getDisplayMetrics().widthPixels - ui.dp(56));
      for (Uploads.Upload upload : items) {
        final Uploads.Upload item = upload;
        View chip =
            AttachmentViews.composerChip(
                this,
                ui,
                images,
                item,
                () -> {
                  uploads.remove(item);
                  renderTray();
                  updateSend();
                },
                () -> {
                  uploads.retry(item);
                  renderTray();
                  updateSend();
                });
        LinearLayout.LayoutParams params =
            new LinearLayout.LayoutParams(
                width, LinearLayout.LayoutParams.WRAP_CONTENT);
        params.setMarginEnd(ui.dp(8));
        chipsRow.addView(chip, params);
      }
    }
    remoteBlocked = Attachments.isRemotePeer(peer);
    if (attachButton != null) attachButton.setVisibility(remoteBlocked ? View.GONE : View.VISIBLE);
    if (remoteBlocked && !trayFlashActive) {
      trayNote.setText(remoteReason());
      trayNote.setVisibility(View.VISIBLE);
    } else if (!trayFlashActive) {
      trayNote.setVisibility(View.GONE);
    }
  }

  private void trayMessage(String message) {
    if (trayNote == null) {
      note(message);
      return;
    }
    trayFlashActive = true;
    trayNote.setText(message);
    trayNote.setVisibility(View.VISIBLE);
    handler.removeCallbacks(trayClear);
    handler.postDelayed(trayClear, 8000);
  }

  private final Runnable trayClear =
      () -> {
        trayFlashActive = false;
        renderTray();
      };

  private void submitThread(String name, List<Model.Message> rows) {
    if (!name.equals(peer) || !screen.equals("chat")) return;
    List<Model.Message> data = rows;
    if (pendingEcho != null) {
      boolean present = false;
      for (Model.Message message : rows) present |= message.id.equals(pendingEcho.id);
      if (!present) {
        data = new ArrayList<>(rows);
        data.add(pendingEcho);
      }
    }
    final List<Model.Message> shown = data;
    boolean first = threadAdapter.getItemCount() == 0;
    boolean atBottom = !chatList.canScrollVertically(1);
    threadAdapter.submitList(
        shown,
        () -> {
          if (chatEmpty != null)
            chatEmpty.setVisibility(shown.isEmpty() ? View.VISIBLE : View.GONE);
          if (restoreChatPosition >= 0
              && name.equals(restoreChatPeer)
              && restoreChatPosition < shown.size()
              && chatList.getLayoutManager() instanceof LinearLayoutManager) {
            ((LinearLayoutManager) chatList.getLayoutManager())
                .scrollToPositionWithOffset(restoreChatPosition, restoreChatOffset);
            restoreChatPosition = -1;
          } else if (first || atBottom || forceBottom) {
            if (!shown.isEmpty()) chatList.scrollToPosition(shown.size() - 1);
          }
          forceBottom = false;
          chatList.post(this::markVisibleRead);
          if (threadOpenStart > 0) {
            long elapsed = SystemClock.uptimeMillis() - threadOpenStart;
            threadOpenStart = 0;
            chatList.post(
                () ->
                    Log.i(
                        PERF_TAG,
                        "thread-open-ms=" + elapsed + " messages=" + shown.size()));
          }
        });
  }

  private void decide(String askId, String option) {
    send(askId, option);
  }

  private void markVisibleRead() {
    if (api == null || !screen.equals("chat") || chatList == null) return;
    RecyclerView.LayoutManager manager = chatList.getLayoutManager();
    if (!(manager instanceof LinearLayoutManager)) return;
    LinearLayoutManager linear = (LinearLayoutManager) manager;
    int first = linear.findFirstVisibleItemPosition();
    int last = linear.findLastVisibleItemPosition();
    List<Model.Message> rows = threadAdapter.getCurrentList();
    if (rows.isEmpty() || first == RecyclerView.NO_POSITION) return;
    List<String> ids = new ArrayList<>();
    for (int i = Math.max(0, first); i <= Math.min(rows.size() - 1, last); i++) {
      Model.Message message = rows.get(i);
      if (message.unread && readRequested.add(message.id)) ids.add(message.id);
    }
    if (ids.isEmpty()) return;
    Api client = api;
    int epoch = connection;
    io.execute(
        () -> {
          for (String id : ids) {
            try {
              client.post("/api/mail/" + Protocol.segment(id) + "/read", new JSONObject());
            } catch (Exception e) {
              post(
                  () -> {
                    if (epoch == connection) handle(e);
                  });
              break;
            }
          }
        });
  }

  private void showAgent() {
    if (peer.isEmpty()) {
      showList();
      return;
    }
    ensureAgentScreen();
    renderChrome();
    agentDetail = agent(peer);
    renderAgent();
    swapBody(agentScreen);
    if (api != null) refresh(AGENT | AGENTS);
  }

  private void ensureAgentScreen() {
    if (agentScreen != null) return;
    agentScreen = new LinearLayout(this);
    agentScreen.setOrientation(LinearLayout.VERTICAL);
    actions(
        agentScreen,
        ui.primary("Messages", () -> open(peer, "chat")),
        ui.quietAction("Refresh", this::userRefresh));
    agentScroll = new ScrollView(this);
    agentScroll.setFillViewport(false);
    agentColumn = new LinearLayout(this);
    agentColumn.setOrientation(LinearLayout.VERTICAL);
    agentColumn.setPadding(0, 0, 0, ui.dp(16));
    agentScroll.addView(agentColumn);
    agentScreen.addView(agentScroll, new LinearLayout.LayoutParams(-1, 0, 1f));
  }

  private void renderAgent() {
    if (agentColumn == null) return;
    JSONObject agent = agentDetail != null ? agentDetail : agent(peer);
    String signature =
        agent == null ? "null" : agent.toString() + Model.stamp(agent.optString("last_activity"));
    if (signature.equals(agentSignature) && agentColumn.getChildCount() > 0) return;
    agentSignature = signature;
    int scrollY = agentScroll.getScrollY();
    agentColumn.removeAllViews();
    if (agent == null) {
      heading(agentColumn, "Agent unavailable", "Refresh to try again.");
      return;
    }
    heading(agentColumn, agent.optString("display", peer), agent.optString("role"));
    String state = agent.optString("state", "unknown");
    TextView statusLabel = ui.label("Status");
    statusLabel.setPadding(0, ui.dp(8), 0, ui.dp(6));
    agentColumn.addView(statusLabel);
    agentColumn.addView(ui.status(Model.statusLabel(state), toneColor(Model.tone(state))));
    JSONObject runtime = agent.optJSONObject("runtime");
    detail("Harness", runtime == null ? "Unknown" : runtime.optString("harness"));
    detail("Model", runtime == null ? "Default" : runtime.optString("model", "Default"));
    detail(
        "Running since",
        agent.optString("started_at").isEmpty()
            ? "Not running"
            : Model.stamp(agent.optString("started_at")));
    detail("Last activity", Model.stamp(agent.optString("last_activity")));
    detail("Directory", agent.optString("dir"));
    if (agent.optBoolean("pending_restart"))
      detail(
          "Restart pending",
          agent.optString("pending_reason", "Configuration changed. Restart to apply."));
    if (!agent.optString("detail").isEmpty()) detail("Activity", agent.optString("detail"));
    if (!agent.optString("last_error").isEmpty())
      detail("Last error", agent.optString("last_error"));
    MaterialButton terminal =
        ui.quietAction("View terminal snapshot", () -> open(peer, "terminal"));
    LinearLayout.LayoutParams terminalParams =
        new LinearLayout.LayoutParams(-2, ui.dp(48));
    terminalParams.topMargin = ui.dp(16);
    agentColumn.addView(terminal, terminalParams);
    actions(
        agentColumn,
        ui.quietAction("Wake", () -> lifecycle("start", "Wake")),
        ui.quietAction("Restart", () -> lifecycle("restart", "Restart")),
        ui.quietAction("Stop", () -> lifecycle("stop", "Stop")));
    if (restoreAgentScroll >= 0) {
      scrollY = restoreAgentScroll;
      restoreAgentScroll = -1;
    }
    final int target = scrollY;
    agentScroll.post(() -> agentScroll.scrollTo(0, target));
  }

  private void detail(String label, String value) {
    TextView title = ui.label(label);
    title.setPadding(0, ui.dp(16), 0, ui.dp(4));
    agentColumn.addView(title);
    TextView text = ui.body(value == null ? "" : value);
    text.setTextIsSelectable(true);
    agentColumn.addView(text);
  }

  private void showTerminal() {
    if (peer.isEmpty()) {
      showList();
      return;
    }
    ensureTerminalScreen();
    renderChrome();
    renderTerminal();
    swapBody(terminalScreen);
    if (api != null) refresh(TERMINAL | AGENTS);
  }

  private void ensureTerminalScreen() {
    if (terminalScreen != null) return;
    terminalScreen = new LinearLayout(this);
    terminalScreen.setOrientation(LinearLayout.VERTICAL);
    actions(
        terminalScreen,
        ui.quietAction("Messages", () -> open(peer, "chat")),
        ui.quietAction("Agent details", () -> open(peer, "agent")),
        ui.quietAction("Refresh snapshot", this::userRefresh));
    TextView note = ui.quiet("Read-only snapshot · refreshes every 10 seconds while visible");
    note.setPadding(0, ui.dp(4), 0, ui.dp(10));
    terminalScreen.addView(note);
    terminalScroll = new ScrollView(this);
    terminalHorizontal = new HorizontalScrollView(this);
    terminalView = ui.mono("");
    terminalView.setTextIsSelectable(true);
    terminalView.setHorizontallyScrolling(true);
    terminalView.setBackground(ui.surface(ui.terminal, Ui.TERMINAL_RADIUS));
    terminalView.setTextColor(ui.terminalInk);
    terminalView.setPadding(ui.dp(12), ui.dp(16), ui.dp(12), ui.dp(16));
    terminalHorizontal.addView(terminalView);
    terminalScroll.addView(terminalHorizontal);
    terminalScreen.addView(terminalScroll, new LinearLayout.LayoutParams(-1, 0, 1f));
  }

  private void renderTerminal() {
    if (terminalView == null) return;
    String text = terminalText;
    if (text.equals(terminalSignature) && terminalView.length() > 0) return;
    terminalSignature = text;
    int scrollY = restoreTerminalScroll >= 0 ? restoreTerminalScroll : terminalScroll.getScrollY();
    int scrollX = terminalHorizontal.getScrollX();
    restoreTerminalScroll = -1;
    terminalView.setText(text.isEmpty() ? "No terminal output. The agent may be stopped." : text);
    terminalScroll.post(() -> terminalScroll.scrollTo(0, scrollY));
    terminalHorizontal.post(() -> terminalHorizontal.scrollTo(scrollX, 0));
  }

  private void showConnect(String reason) {
    screen = "connect";
    peer = "";
    activeSwipe = null;
    ensureConnectScreen();
    renderChrome();
    swapBody(connectScreen);
    urlField.setText(savedServer);
    if (reason != null && !reason.isEmpty()) banner.setText(reason);
    else if (api == null) banner.setText("Your agents, wherever you are.");
  }

  private void ensureConnectScreen() {
    if (connectScreen != null) return;
    connectScreen = new LinearLayout(this);
    connectScreen.setOrientation(LinearLayout.VERTICAL);
    ScrollView scroll = new ScrollView(this);
    LinearLayout form = new LinearLayout(this);
    form.setOrientation(LinearLayout.VERTICAL);
    form.setPadding(0, 0, 0, ui.dp(24));
    scroll.addView(form);
    connectScreen.addView(scroll, new LinearLayout.LayoutParams(-1, 0, 1f));
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
    urlField.setText(savedServer);
    urlField.setHint(Protocol.URL_HINT);
    codeField =
        field(
            form,
            "8-character pairing code",
            android.text.InputType.TYPE_CLASS_TEXT
                | android.text.InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS);
    codeField.setFilters(new InputFilter[] {new InputFilter.LengthFilter(8)});
    form.addView(ui.primary("Pair device", this::pair));
    MaterialButton scan =
        ui.quietAction(
            "Scan dashboard QR code",
            () ->
                scanner.launch(
                    new ScanOptions()
                        .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                        .setPrompt("Scan the lolad Devices pairing QR")
                        .setBeepEnabled(false)
                        .setOrientationLocked(false)));
    LinearLayout.LayoutParams scanParams = new LinearLayout.LayoutParams(-2, ui.dp(48));
    scanParams.topMargin = ui.dp(8);
    form.addView(scan, scanParams);
    TextView fallback = ui.label("Already have a device token?");
    fallback.setPadding(0, ui.dp(24), 0, ui.dp(8));
    form.addView(fallback);
    tokenField =
        field(
            form,
            "Device token (manual fallback)",
            android.text.InputType.TYPE_CLASS_TEXT
                | android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD);
    form.addView(ui.quietAction("Connect with token", this::connectToken));
  }

  private EditText field(LinearLayout parent, String label, int input) {
    TextInputLayout wrap = new TextInputLayout(this);
    wrap.setHint(label);
    wrap.setBoxBackgroundMode(TextInputLayout.BOX_BACKGROUND_OUTLINE);
    wrap.setBoxCornerRadii(
        ui.dp(Ui.FIELD_RADIUS),
        ui.dp(Ui.FIELD_RADIUS),
        ui.dp(Ui.FIELD_RADIUS),
        ui.dp(Ui.FIELD_RADIUS));
    TextInputEditText edit = new TextInputEditText(wrap.getContext());
    edit.setTextSize(android.util.TypedValue.COMPLEX_UNIT_SP, Ui.BODY);
    edit.setInputType(input);
    edit.setTypeface(Typeface.DEFAULT);
    edit.setMinHeight(ui.dp(56));
    wrap.addView(edit);
    LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2);
    params.bottomMargin = ui.dp(16);
    parent.addView(wrap, params);
    return edit;
  }

  private void onBack() {
    WindowInsetsCompat insets = ViewCompat.getRootWindowInsets(root);
    if (insets != null && insets.isVisible(WindowInsetsCompat.Type.ime())) {
      WindowCompat.getInsetsController(getWindow(), root).hide(WindowInsetsCompat.Type.ime());
      return;
    }
    rememberDraft();
    if (!history.isEmpty()) {
      String[] previous = history.remove(history.size() - 1).split("\\|", -1);
      peer = previous[0];
      screen = previous[1];
      showCurrent();
      if (api != null) refresh(scopesFor(screen));
      return;
    }
    if (!peer.isEmpty()) {
      peer = "";
      screen = "inbox";
      showList();
      if (api != null) refresh(AGENTS | CONVERSATIONS);
      return;
    }
    finish();
  }

  private void openConversation(String name) {
    open(name, "chat");
  }

  private void openAgent(String name) {
    open(name, "agent");
  }

  private void open(String name, String mode) {
    if (name == null || name.isEmpty()) return;
    rememberDraft();
    if (!name.equals(peer) || !mode.equals(screen)) history.add(peer + "|" + screen);
    if (!peer.equals(name)) {
      draft = "";
      if (draftField != null) draftField.setText("");
      agentDetail = null;
      agentSignature = "";
      terminalText = "";
      terminalSignature = "";
      focusApplied = false;
    }
    peer = name;
    screen = mode;
    showCurrent();
    if (api != null) refresh(scopesFor(mode));
  }

  private void rememberDraft() {
    if (draftField != null) draft = draftField.getText().toString();
  }

  private void updateSend() {
    boolean ready = api != null && !uploads.finished(peer).isEmpty();
    boolean uploading = uploads.busy(peer);
    boolean typed = draftField != null && !draftField.getText().toString().trim().isEmpty();
    if (sendButton != null)
      sendButton.setEnabled(api != null && !sending && !uploading && (typed || ready));
    if (attachButton != null)
      attachButton.setEnabled(
          canAttach() && uploads.count(peer) < Attachments.MAX_FILES);
    if (threadAdapter != null) threadAdapter.setEnabled(!sending);
  }

  private JSONObject parentFor(String askId, String decision) {
    JSONArray thread = threadJsonCache.get(peer);
    if (askId != null) {
      if (thread != null)
        for (int i = 0; i < thread.length(); i++) {
          JSONObject message = thread.optJSONObject(i);
          if (message != null && askId.equals(message.optString("id"))) return message;
        }
      return null;
    }
    if (thread != null)
      for (int i = thread.length() - 1; i >= 0; i--) {
        JSONObject message = thread.optJSONObject(i);
        if (message != null && !message.optString("from_name").equals(me)) return message;
      }
    return null;
  }

  private void send(String askId, String decision) {
    if (sending || api == null || System.currentTimeMillis() < retryAt) return;
    rememberDraft();
    String value = draft.trim();
    if (uploads.busy(peer)) {
      trayMessage("Wait for the upload to finish before sending.");
      return;
    }
    List<Attachment> ready = decision == null ? uploads.finished(peer) : new ArrayList<>();
    List<String> ids = new ArrayList<>();
    List<String> names = new ArrayList<>();
    for (Attachment attachment : ready) {
      ids.add(attachment.id);
      names.add(attachment.filename);
    }
    JSONObject parent = parentFor(askId, decision);
    final JSONObject payload;
    try {
      payload =
          Protocol.outgoing(
              peer, value, parent, decision, ids, Attachments.derivedSubject(names));
    } catch (Exception e) {
      trayMessage(e.getMessage());
      return;
    }
    sending = true;
    updateSend();
    haptic();
    Api client = api;
    int epoch = connection;
    String selected = peer;
    Model.Message echo = null;
    if (decision == null && !ready.isEmpty()) {
      echo =
          Model.Message.optimistic(
              "pending-" + (++pendingEchoSeq), payload.optString("subject"), value, ready);
      pendingEcho = echo;
      submitThread(peer, threadAdapter.getCurrentList());
      if (chatList != null) chatList.post(() -> {
        if (threadAdapter.getItemCount() > 0)
          chatList.scrollToPosition(threadAdapter.getItemCount() - 1);
      });
    }
    final Model.Message optimistic = echo;
    io.execute(
        () -> {
          try {
            client.post("/api/mail/send", payload);
            post(
                () -> {
                  if (epoch != connection) return;
                  sending = false;
                  if (pendingEcho == optimistic) pendingEcho = null;
                  if (selected.equals(peer) && decision == null) {
                    draft = "";
                    if (draftField != null) draftField.setText("");
                    if (!ids.isEmpty()) uploads.clear(peer);
                  }
                  renderTray();
                  updateSend();
                  forceBottom = true;
                  refresh(CHAT | CONVERSATIONS);
                });
          } catch (Exception e) {
            post(
                () -> {
                  if (epoch != connection) return;
                  sending = false;
                  if (pendingEcho == optimistic) pendingEcho = null;
                  if (optimistic != null && threadAdapter != null) {
                    List<Model.Message> rows = new ArrayList<>(threadAdapter.getCurrentList());
                    for (int i = rows.size() - 1; i >= 0; i--) {
                      if (rows.get(i).id.equals(optimistic.id)) rows.remove(i);
                    }
                    threadAdapter.submitList(rows);
                  }
                  updateSend();
                  handle(e);
                  String extra =
                      ids.isEmpty()
                          ? ""
                          : " "
                              + Attachments.sendError(
                                  e instanceof Api.Failure ? ((Api.Failure) e).status : 0);
                  note(
                      "Send wasn’t confirmed. Refresh the conversation before retrying; your draft"
                          + " is saved."
                          + extra);
                  refresh(CHAT | CONVERSATIONS);
                });
          }
        });
  }

  private void haptic() {
    View target = sendButton != null ? sendButton : root;
    if (target == null) return;
    target.performHapticFeedback(
        Build.VERSION.SDK_INT >= 30
            ? HapticFeedbackConstants.CONFIRM
            : HapticFeedbackConstants.KEYBOARD_TAP);
  }

  private void openAttachment(Attachment attachment, View source) {
    if (api == null || attachment == null) return;
    if (attachment.canInline()) {
      ImagePreview.show(
          this, ui, images, api, peer, attachment, () -> saveAttachment(attachment));
      return;
    }
    saveAttachment(attachment);
  }

  private void saveAttachment(Attachment attachment) {
    if (api == null) return;
    downloadTarget = attachment;
    String suggested = Attachments.safe(attachment.filename).replace('/', '_');
    createDocument.launch(suggested.isEmpty() ? "download" : suggested);
  }

  private void beginDownload(Attachment attachment, Uri destination) {
    if (api == null) return;
    OutputStream sink;
    try {
      sink = getContentResolver().openOutputStream(destination);
    } catch (Exception e) {
      sink = null;
    }
    if (sink == null) {
      note("That location can’t be written. Choose another one.");
      return;
    }
    final OutputStream output = sink;
    ProgressBar bar = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
    bar.setMax(100);
    TextView label = ui.quiet("Starting…");
    LinearLayout box = new LinearLayout(this);
    box.setOrientation(LinearLayout.VERTICAL);
    box.setPadding(ui.dp(24), ui.dp(8), ui.dp(24), 0);
    box.addView(label);
    LinearLayout.LayoutParams barParams =
        new LinearLayout.LayoutParams(-1, LinearLayout.LayoutParams.WRAP_CONTENT);
    barParams.topMargin = ui.dp(12);
    box.addView(bar, barParams);
    final Uri target = destination;
    downloadDialog =
        new MaterialAlertDialogBuilder(this)
            .setTitle("Saving " + attachment.shortName())
            .setView(box)
            .setNegativeButton("Cancel", (d, w) -> cancelDownload(target))
            .setOnCancelListener(d -> cancelDownload(target))
            .create();
    downloadDialog.show();
    downloadCall =
        api.download(
            peer,
            attachment.id,
            false,
            output,
            (written, total) -> {
              int percent = total > 0 ? (int) Math.min(100, written * 100 / total) : -1;
              post(
                  () -> {
                    if (percent >= 0) bar.setProgress(percent);
                    label.setText(
                        total > 0
                            ? Attachments.humanSize(written) + " of " + Attachments.humanSize(total)
                            : Attachments.humanSize(written) + " received");
                  });
            },
            () ->
                post(
                    () -> {
                      dismissDownload();
                      note("Saved " + attachment.filename);
                    }),
            error ->
                post(
                    () -> {
                      dismissDownload();
                      handle(error);
                    }));
  }

  private void cancelDownload(Uri destination) {
    if (downloadCall != null) downloadCall.cancel();
    downloadCall = null;
    dismissDownload();
    io.execute(
        () -> {
          try {
            DocumentsContract.deleteDocument(getContentResolver(), destination);
          } catch (Exception ignored) {
            // A provider may keep the empty document; nothing else to clean up.
          }
        });
    note("Download cancelled.");
  }

  private void dismissDownload() {
    downloadCall = null;
    if (downloadDialog != null) {
      downloadDialog.dismiss();
      downloadDialog = null;
    }
  }

  private void lifecycle(String action, String label) {
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
              io.execute(
                  () -> {
                    try {
                      client.post(
                          "/api/agents/" + Protocol.segment(name) + "/" + action, new JSONObject());
                      post(
                          () -> {
                            if (epoch == connection) refresh(AGENT | AGENTS);
                          });
                    } catch (Exception e) {
                      post(
                          () -> {
                            if (epoch == connection) handle(e);
                          });
                    }
                  });
            })
        .show();
  }

  private void confirmDisconnect() {
    new MaterialAlertDialogBuilder(this)
        .setTitle("Disconnect this device?")
        .setMessage("This removes the saved device token from this phone.")
        .setNegativeButton("Cancel", null)
        .setPositiveButton("Disconnect", (d, w) -> disconnect(""))
        .show();
  }

  private void disconnect(String reason) {
    if (api == null && screen.equals("connect")) {
      if (reason != null && !reason.isEmpty()) banner.setText(reason);
      return;
    }
    if (stream != null) {
      stream.cancel();
      stream = null;
    }
    handler.removeCallbacks(poll);
    handler.removeCallbacks(reconnectStream);
    handler.removeCallbacks(sseFlush);
    sseScheduled = false;
    api = null;
    sending = false;
    busy = false;
    rateLimited = false;
    retryAt = 0;
    me = "";
    agentsJson = new JSONArray();
    conversationsJson = new JSONArray();
    synchronized (askMap) {
      askMap.clear();
    }
    askCache.clear();
    threadCache.clear();
    threadJsonCache.clear();
    readRequested.clear();
    history.clear();
    peer = "";
    screen = "connect";
    draft = "";
    savedServer = "";
    pendingScope = 0;
    sseScope = 0;
    cacheSignature = 0;
    chatPeer = null;
    forceBottom = false;
    terminalText = "";
    terminalSignature = "";
    agentDetail = null;
    agentSignature = "";
    pendingEcho = null;
    trayFlashActive = false;
    remoteBlocked = false;
    handler.removeCallbacks(trayClear);
    uploads.clearAll();
    conversationAdapter.submitList(new ArrayList<>());
    agentAdapter.submitList(new ArrayList<>());
    if (threadAdapter != null) threadAdapter.submitList(new ArrayList<>());
    if (draftField != null) draftField.setText("");
    if (chipsRow != null) chipsRow.removeAllViews();
    if (tray != null) tray.setVisibility(View.GONE);
    if (trayLabel != null) trayLabel.setVisibility(View.GONE);
    connection++;
    showConnect(reason);
    io.execute(() -> images.clear());
    io.execute(
        () -> {
          try {
            if (session == null) session = new Session(new KeystoreStorage(this));
            session.disconnect();
          } catch (Exception ignored) {
          }
          if (cache == null) cache = new LocalCache(this);
          cache.clear();
        });
  }

  private void handle(Exception e) {
    if (e instanceof Api.Failure) {
      Api.Failure failure = (Api.Failure) e;
      if (failure.status == 401 && api != null) {
        disconnect(failure.getMessage());
        return;
      }
      if (failure.status == 429) {
        rateLimited = true;
        retryAt = System.currentTimeMillis() + failure.retryAfter * 1000;
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
    stopSpinners();
  }

  private void pairLink(String link) {
    getIntent().setData(null);
    if (api != null) {
      note("Disconnect before pairing another device.");
      return;
    }
    try {
      String[] pair = Protocol.pairing(link);
      if (urlField == null) {
        pendingLink = link;
        return;
      }
      urlField.setText(pair[0]);
      codeField.setText(pair[1]);
      note("Pairing link ready. Tap Pair device.");
    } catch (Exception e) {
      note(e.getMessage());
    }
  }

  private void pair() {
    if (busy || urlField == null) return;
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
    busy = true;
    banner.setText("Pairing this device…");
    io.execute(
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
            post(
                () -> {
                  busy = false;
                  establish(url, result.optString("token"));
                });
          } catch (Exception e) {
            post(
                () -> {
                  busy = false;
                  handle(e);
                });
          }
        });
  }

  private void connectToken() {
    if (busy || urlField == null) return;
    if (System.currentTimeMillis() < retryAt) {
      note("Wait before trying another token.");
      return;
    }
    rateLimited = false;
    try {
      final String url = Protocol.server(urlField.getText().toString());
      final String token = tokenField.getText().toString().trim();
      if (!token.matches("lol_[A-Za-z0-9_-]+"))
        throw new IllegalArgumentException("Paste a lol_ device token.");
      busy = true;
      banner.setText("Checking device…");
      io.execute(
          () -> {
            try {
              new Api(url, token).object("/api/me");
              post(
                  () -> {
                    busy = false;
                    establish(url, token);
                  });
            } catch (Exception e) {
              post(
                  () -> {
                    busy = false;
                    handle(e);
                  });
            }
          });
    } catch (Exception e) {
      handle(e);
    }
  }

  private void establish(String url, String token) {
    if (url.isEmpty() || token.isEmpty()) {
      handle(new IOException("The server did not return a device token."));
      return;
    }
    busy = true;
    io.execute(
        () -> {
          Session loaded;
          Api client;
          try {
            loaded = session == null ? new Session(new KeystoreStorage(this)) : session;
            loaded.connect(url, token);
            client = new Api(url, token);
          } catch (Exception e) {
            post(
                () -> {
                  busy = false;
                  handle(e);
                });
            return;
          }
          post(
              () -> {
                busy = false;
                rateLimited = false;
                retryAt = 0;
                connection++;
                session = loaded;
                api = client;
                savedServer = url;
                me = "";
                agentsJson = new JSONArray();
                conversationsJson = new JSONArray();
                synchronized (askMap) {
                  askMap.clear();
                }
                askCache.clear();
                threadCache.clear();
                threadJsonCache.clear();
                readRequested.clear();
                history.clear();
                peer = "";
                screen = "inbox";
                draft = "";
                chatPeer = null;
                sending = false;
                pendingScope = 0;
                sseScope = 0;
                cacheSignature = 0;
                pendingEcho = null;
                uploads.clearAll();
                io.execute(() -> images.clear());
                agentDetail = null;
                agentSignature = "";
                terminalText = "";
                terminalSignature = "";
                conversationAdapter.submitList(new ArrayList<>());
                agentAdapter.submitList(new ArrayList<>());
                if (threadAdapter != null) threadAdapter.submitList(new ArrayList<>());
                if (draftField != null) draftField.setText("");
                showList();
                startRealtime();
                refresh(ALL);
              });
        });
  }

  private void startRealtime() {
    handler.removeCallbacks(poll);
    if (foreground && api != null) handler.postDelayed(poll, 10000);
    startStream();
  }

  private void userRefresh() {
    if (System.currentTimeMillis() < retryAt) {
      note("Wait before refreshing again.");
      stopSpinners();
      return;
    }
    rateLimited = false;
    if (api == null) {
      stopSpinners();
      return;
    }
    refresh(scopesFor(screen));
    startStream();
  }

  private int scopesFor(String mode) {
    switch (mode) {
      case "chat":
        return AGENTS | CONVERSATIONS | CHAT;
      case "agent":
        return AGENTS | CONVERSATIONS | AGENT;
      case "terminal":
        return AGENTS | CONVERSATIONS | TERMINAL;
      case "connect":
        return 0;
      default:
        return AGENTS | CONVERSATIONS;
    }
  }

  private void refresh(int scope) {
    if (scope == 0 || api == null || !foreground) {
      stopSpinners();
      return;
    }
    if (rateLimited || System.currentTimeMillis() < retryAt) {
      stopSpinners();
      return;
    }
    pendingScope |= scope;
    pump();
  }

  private void pump() {
    if (fetching) return;
    int scope = pendingScope;
    pendingScope = 0;
    if (scope == 0) return;
    final Api client = api;
    final int epoch = connection;
    fetching = true;
    io.execute(
        () -> {
          Fetched fetched = null;
          Exception failure = null;
          try {
            fetched = fetch(scope, client, epoch);
          } catch (Exception e) {
            failure = e;
          }
          final Fetched result = fetched;
          final Exception error = failure;
          post(
              () -> {
                fetching = false;
                if (epoch == connection) {
                  if (error != null) handle(error);
                  else if (result != null) apply(result);
                }
                if (pendingScope != 0 && api != null && foreground) pump();
              });
        });
  }

  private Fetched fetch(int scope, Api client, int epoch) throws Exception {
    Fetched fetched = new Fetched();
    fetched.scope = scope;
    fetched.epoch = epoch;
    fetched.peer = peer;
    fetched.screen = screen;
    String user = me;
    if (user.isEmpty()) {
      user = client.object("/api/me").getString("name");
      fetched.me = user;
    }
    if ((scope & AGENTS) != 0) fetched.agents = client.array("/api/agents");
    if ((scope & CONVERSATIONS) != 0) fetched.conversations = client.array("/api/mail/conversations");

    if ((scope & CONVERSATIONS) != 0) {
      for (int i = 0; i < fetched.conversations.length(); i++) {
        JSONObject conversation = fetched.conversations.optJSONObject(i);
        if (conversation == null) continue;
        String name = conversation.optString("peer");
        if (name.isEmpty()) continue;
        if (name.equals(fetched.peer) && (scope & CHAT) != 0) continue;
        JSONObject last = conversation.optJSONObject("last");
        String lastId = last == null ? "" : last.optString("id");
        AskState known = askCache.get(name);
        if (known != null && known.lastId.equals(lastId)) {
          putAsk(name, known.asks);
          continue;
        }
        JSONArray thread =
            client.array("/api/mail/conversation/" + Protocol.segment(name) + "?limit=1000");
        int asks = countAsks(thread, user);
        putAsk(name, asks);
        askCache.put(name, new AskState(lastId, asks));
      }
    }

    if ((scope & CHAT) != 0 && !fetched.peer.isEmpty()) {
      JSONArray thread =
          client.array(
              "/api/mail/conversation/" + Protocol.segment(fetched.peer) + "?limit=1000");
      fetched.thread = thread;
      fetched.threadPeer = fetched.peer;
      fetched.messages = Model.messages(thread, user, displayFrom(fetched.agents, fetched.peer));
      int asks = countAsks(thread, user);
      putAsk(fetched.peer, asks);
      askCache.put(fetched.peer, new AskState(lastId(thread), asks));
    }

    if ((scope & AGENT) != 0 && !fetched.peer.isEmpty())
      fetched.agent =
          client.object("/api/agents/" + Protocol.segment(fetched.peer));
    if ((scope & TERMINAL) != 0 && !fetched.peer.isEmpty())
      fetched.capture =
          Protocol.stripAnsi(
              client
                  .object(
                      "/api/agents/"
                          + Protocol.segment(fetched.peer)
                          + "/capture?lines=150")
                  .optString("capture"));

    if (fetched.conversations != null || (scope & CHAT) != 0) {
      JSONArray agents = fetched.agents != null ? fetched.agents : agentsJson;
      JSONArray conversations =
          fetched.conversations != null ? fetched.conversations : conversationsJson;
      Map<String, Integer> asks = snapshotAsks();
      fetched.conversationRows = Model.conversations(conversations, asks);
      fetched.agentRows = Model.agents(agents, asks, unreadByPeer(conversations));
    }
    fetched.asks = snapshotAsks();

    if (fetched.agents != null || fetched.conversations != null || fetched.thread != null) {
      JSONArray agents = fetched.agents != null ? fetched.agents : agentsJson;
      JSONArray conversations =
          fetched.conversations != null ? fetched.conversations : conversationsJson;
      int signature = (agents.toString() + "|" + conversations.toString() + "|" + fetched.asks).hashCode();
      if (signature != cacheSignature) {
        cacheSignature = signature;
        if (cache != null) cache.write(agents, conversations, fetched.asks);
      }
    }
    return fetched;
  }

  private void apply(Fetched fetched) {
    if (fetched.epoch != connection) return;
    if (fetched.me != null && !fetched.me.isEmpty()) me = fetched.me;
    if (fetched.agents != null) agentsJson = fetched.agents;
    if (fetched.conversations != null) conversationsJson = fetched.conversations;
    if (fetched.conversationRows != null) conversationAdapter.submitList(fetched.conversationRows);
    if (fetched.agentRows != null) agentAdapter.submitList(fetched.agentRows);
    if (fetched.thread != null) {
      threadJsonCache.put(fetched.threadPeer, fetched.thread);
      threadCache.put(fetched.threadPeer, fetched.messages);
      if (fetched.threadPeer.equals(peer) && screen.equals("chat")) {
        if (threadAdapter != null) threadAdapter.setContext(api, peer, images);
        submitThread(fetched.threadPeer, fetched.messages);
      }
    }
    if (fetched.capture != null && fetched.peer.equals(peer) && screen.equals("terminal")) {
      terminalText = fetched.capture;
      renderTerminal();
    }
    if (fetched.agent != null && fetched.peer.equals(peer) && screen.equals("agent")) {
      agentDetail = fetched.agent;
      renderAgent();
    }
    if (fetched.conversationRows != null) updateEmpty();
    if (inboxList != null) inboxList.post(this::restoreListPositions);
    stopSpinners();
    banner.setText(
        stream != null
            ? "Connected · live updates"
            : "Connected · refreshing every 10 seconds");
    if (!networkLogged && fetched.conversations != null) {
      networkLogged = true;
      Log.i(START_TAG, "network-screen-ms=" + (SystemClock.uptimeMillis() - startedAt));
      logNextDraw("network-first-frame-ms");
    }
  }

  private void logNextDraw(String label) {
    final View decor = getWindow().getDecorView();
    decor
        .getViewTreeObserver()
        .addOnPreDrawListener(
            new ViewTreeObserver.OnPreDrawListener() {
              @Override
              public boolean onPreDraw() {
                ViewTreeObserver observer = decor.getViewTreeObserver();
                if (observer.isAlive()) observer.removeOnPreDrawListener(this);
                Log.i(START_TAG, label + "=" + (SystemClock.uptimeMillis() - startedAt));
                return true;
              }
            });
  }

  private void stopSpinners() {
    if (inboxSwipe != null) inboxSwipe.setRefreshing(false);
    if (agentsSwipe != null) agentsSwipe.setRefreshing(false);
  }

  private void startStream() {
    if (!foreground
        || api == null
        || stream != null
        || rateLimited
        || System.currentTimeMillis() < retryAt) return;
    final int epoch = connection;
    stream =
        api.stream(
            this::onSse,
            e ->
                post(
                    () -> {
                      if (epoch != connection || !foreground) return;
                      stream = null;
                      if (e instanceof Api.Failure
                          && (((Api.Failure) e).status == 401
                              || ((Api.Failure) e).status == 429)) {
                        handle(e);
                        return;
                      }
                      banner.setText("Live updates paused · refreshing every 10 seconds");
                      handler.postDelayed(reconnectStream, reconnect);
                      reconnect = Math.min(reconnect * 2, 30000);
                    }),
            () ->
                post(
                    () -> {
                      if (epoch != connection || !foreground) return;
                      reconnect = 1000;
                      banner.setText("Connected · live updates");
                      refresh(scopesFor(screen));
                    }));
  }

  private void onSse(String kind, String data) {
    int scope;
    try {
      JSONObject event = new JSONObject(data);
      String eventKind = event.optString("kind", kind);
      String agent = event.optString("agent");
      if (eventKind.equals("mail") || eventKind.equals("notified")) {
        scope = CONVERSATIONS;
        if (agent.equals(peer) && screen.equals("chat")) scope |= CHAT;
      } else {
        scope = AGENTS;
        if (agent.equals(peer)) {
          if (screen.equals("agent")) scope |= AGENT;
          if (screen.equals("terminal")) scope |= TERMINAL;
        }
      }
    } catch (Exception e) {
      scope = AGENTS | CONVERSATIONS;
    }
    final int bits = scope;
    post(
        () -> {
          sseScope |= bits;
          if (!sseScheduled) {
            sseScheduled = true;
            handler.postDelayed(sseFlush, 150);
          }
        });
  }

  private String display(String name) {
    JSONObject agent = agent(name);
    if (agent != null) return agent.optString("display", name);
    for (int i = 0; i < conversationsJson.length(); i++) {
      JSONObject conversation = conversationsJson.optJSONObject(i);
      if (conversation != null && name.equals(conversation.optString("peer")))
        return conversation.optString("display", name);
    }
    return name;
  }

  private String displayFrom(JSONArray agents, String name) {
    if (agents != null)
      for (int i = 0; i < agents.length(); i++) {
        JSONObject agent = agents.optJSONObject(i);
        if (agent != null && name.equals(agent.optString("name")))
          return agent.optString("display", name);
      }
    return name;
  }

  private JSONObject agent(String name) {
    for (int i = 0; i < agentsJson.length(); i++) {
      JSONObject agent = agentsJson.optJSONObject(i);
      if (agent != null && name.equals(agent.optString("name"))) return agent;
    }
    return null;
  }

  private static Map<String, Integer> unreadByPeer(JSONArray conversations) {
    Map<String, Integer> unread = new HashMap<>();
    if (conversations == null) return unread;
    for (int i = 0; i < conversations.length(); i++) {
      JSONObject conversation = conversations.optJSONObject(i);
      if (conversation == null) continue;
      unread.put(conversation.optString("peer"), conversation.optInt("unread"));
    }
    return unread;
  }

  private static int countAsks(JSONArray thread, String user) {
    int pending = 0;
    if (thread == null) return 0;
    for (int i = 0; i < thread.length(); i++) {
      JSONObject mail = thread.optJSONObject(i);
      if (mail == null) continue;
      if (mail.optString("from_name").equals(user)) pending = 0;
      else if (Protocol.kind(mail).equals("ask")) pending++;
    }
    return pending;
  }

  private static String lastId(JSONArray thread) {
    if (thread == null || thread.length() == 0) return "";
    JSONObject last = thread.optJSONObject(thread.length() - 1);
    return last == null ? "" : last.optString("id");
  }

  private void putAsk(String name, int count) {
    synchronized (askMap) {
      askMap.put(name, count);
    }
  }

  private Map<String, Integer> snapshotAsks() {
    synchronized (askMap) {
      return new HashMap<>(askMap);
    }
  }

  private int toneColor(Model.Tone tone) {
    switch (tone) {
      case AWAKE:
        return ui.awake;
      case ATTENTION:
        return ui.attention;
      case ERROR:
        return ui.error;
      default:
        return ui.muted;
    }
  }

  private void heading(LinearLayout parent, String title, String detail) {
    TextView heading = ui.headline(title);
    parent.addView(heading);
    TextView sub = ui.text(detail == null ? "" : detail, Ui.LABEL, ui.muted, 400, 2);
    sub.setPadding(0, ui.dp(6), 0, ui.dp(20));
    parent.addView(sub);
  }

  private void actions(LinearLayout parent, View... buttons) {
    ActionRows rows = new ActionRows(this, ui.dp(8));
    for (View button : buttons)
      rows.addView(button, new ViewGroup.LayoutParams(-2, -2));
    LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2);
    params.topMargin = ui.dp(4);
    params.bottomMargin = ui.dp(4);
    parent.addView(rows, params);
  }

  private void note(String message) {
    if (root != null) Snackbar.make(root, message, Snackbar.LENGTH_LONG).show();
  }

  ActivityResultLauncher<ScanOptions> scanner;

  private volatile int cacheSignature;

  static final class AskState {
    final String lastId;
    final int asks;

    AskState(String lastId, int asks) {
      this.lastId = lastId;
      this.asks = asks;
    }
  }

  static final class Fetched {
    int scope, epoch;
    String peer, screen, me;
    JSONArray agents, conversations, thread;
    String threadPeer;
    List<Model.Message> messages;
    List<Model.Conversation> conversationRows;
    List<Model.Agent> agentRows;
    JSONObject agent;
    String capture;
    Map<String, Integer> asks;
  }
}
