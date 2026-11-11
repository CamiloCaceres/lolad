package dev.caceres.lolad;

import android.content.ContentResolver;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicReference;
import okhttp3.Call;
import org.json.JSONObject;

/**
 * Uploads start the moment a file is picked and run one request per file,
 * streamed from its content URI. The queue outlives an activity recreation so a
 * rotation never restarts or loses an upload; it is cleared on disconnect.
 * Every method here runs on the main thread; progress arrives from the pool.
 */
final class Uploads {
  interface Watcher {
    void uploadsChanged(String peer);
  }

  enum State {
    UPLOADING,
    FAILED,
    DONE
  }

  static final int ADDED = 0, LIMIT = 1, TOO_LARGE = 2, UNAVAILABLE = 3;

  static final class Upload {
    final String key = UUID.randomUUID().toString();
    final String peer, filename, contentType;
    final Uri uri;
    final long size;
    final Api api;
    volatile State state = State.UPLOADING;
    volatile int percent;
    volatile String error = "";
    volatile Attachment attachment;
    volatile Api.Failure failure;
    volatile AtomicReference<Call> handle;
    volatile boolean removed;

    Upload(String peer, String filename, String contentType, Uri uri, long size, Api api) {
      this.peer = peer;
      this.filename = filename;
      this.contentType = contentType;
      this.uri = uri;
      this.size = size;
      this.api = api;
    }

    boolean done() {
      return state == State.DONE && attachment != null;
    }

    boolean uploading() {
      return state == State.UPLOADING;
    }

    String id() {
      return attachment == null ? "" : attachment.id;
    }

    Attachment asAttachment() {
      if (attachment != null) return attachment;
      return Attachment.pending(filename, contentType, size, uri.toString());
    }
  }

  private static Uploads shared;

  /** One queue per process: a rotation must never restart or lose an upload. */
  static synchronized Uploads shared(Context context) {
    if (shared == null) shared = new Uploads(context);
    return shared;
  }

  private final Context app;
  private final ExecutorService pool;
  private final Handler main = new Handler(Looper.getMainLooper());
  private final Map<String, List<Upload>> byPeer = new LinkedHashMap<>();
  private volatile Watcher watcher;

  Uploads(Context context) {
    app = context.getApplicationContext();
    pool =
        Executors.newFixedThreadPool(
            2,
            runnable -> {
              Thread thread = new Thread(runnable, "lolad-upload");
              thread.setDaemon(true);
              return thread;
            });
  }

  void setWatcher(Watcher watcher) {
    this.watcher = watcher;
  }

  void clearWatcher(Watcher watcher) {
    if (this.watcher == watcher) this.watcher = null;
  }

  List<Upload> list(String peer) {
    List<Upload> items = byPeer.get(peer);
    return items == null ? Collections.emptyList() : new ArrayList<>(items);
  }

  int count(String peer) {
    List<Upload> items = byPeer.get(peer);
    return items == null ? 0 : items.size();
  }

  boolean busy(String peer) {
    for (Upload upload : list(peer)) if (upload.uploading()) return true;
    return false;
  }

  List<Attachment> finished(String peer) {
    List<Attachment> out = new ArrayList<>();
    for (Upload upload : list(peer)) {
      if (!upload.done()) continue;
      Attachment stored = upload.attachment;
      out.add(
          new Attachment(
              stored.id,
              stored.filename,
              stored.contentType,
              stored.size,
              stored.digest,
              stored.scanStatus,
              upload.uri.toString()));
    }
    return out;
  }

  int add(Api api, String peer, Uri uri, boolean pastedImage, long now) {
    if (api == null || peer == null || peer.isEmpty() || uri == null) return UNAVAILABLE;
    List<Upload> items = byPeer.get(peer);
    if (items != null && items.size() >= Attachments.MAX_FILES) return LIMIT;
    ContentResolver resolver = app.getContentResolver();
    String type = "";
    String display = "";
    long size = -1;
    try {
      type = resolver.getType(uri);
      try (Cursor cursor = resolver.query(uri, null, null, null, null)) {
        if (cursor != null && cursor.moveToFirst()) {
          int nameColumn = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME);
          int sizeColumn = cursor.getColumnIndex(OpenableColumns.SIZE);
          if (nameColumn >= 0) display = cursor.getString(nameColumn);
          if (sizeColumn >= 0 && !cursor.isNull(sizeColumn)) size = cursor.getLong(sizeColumn);
        }
      }
    } catch (Exception ignored) {
      // A provider that cannot answer still streams; the server sees the bytes.
    }
    if (type == null || type.isEmpty()) type = "application/octet-stream";
    String name =
        pastedImage
            ? Attachments.pastedName(now)
            : Attachments.pickName(display, type, now);
    if (size > Attachments.MAX_FILE_BYTES) return TOO_LARGE;
    Upload upload = new Upload(peer, name, type, uri, size, api);
    if (items == null) {
      items = new ArrayList<>();
      byPeer.put(peer, items);
    }
    items.add(upload);
    notifyWatcher(peer);
    start(upload);
    return ADDED;
  }

  void retry(Upload upload) {
    if (upload == null || upload.state != State.FAILED) return;
    start(upload);
  }

  void remove(Upload upload) {
    if (upload == null) return;
    upload.removed = true;
    Cancel.cancel(upload);
    List<Upload> items = byPeer.get(upload.peer);
    if (items != null) {
      items.remove(upload);
      if (items.isEmpty()) byPeer.remove(upload.peer);
    }
    notifyWatcher(upload.peer);
  }

  void clear(String peer) {
    List<Upload> items = byPeer.remove(peer);
    if (items == null) return;
    for (Upload upload : items) {
      upload.removed = true;
      Cancel.cancel(upload);
    }
    notifyWatcher(peer);
  }

  void clearAll() {
    for (String peer : new ArrayList<>(byPeer.keySet())) clear(peer);
  }

  /** Only for an explicit disconnect; the process-wide queue otherwise survives. */
  void shutdown() {
    clearAll();
    watcher = null;
    pool.shutdownNow();
  }

  private void start(Upload upload) {
    upload.state = State.UPLOADING;
    upload.error = "";
    upload.failure = null;
    upload.percent = 0;
    upload.removed = false;
    notifyWatcher(upload.peer);
    final AtomicReference<Call> handle = new AtomicReference<>();
    upload.handle = handle;
    pool.execute(
        () -> {
          try {
            long length = upload.size;
            okhttp3.RequestBody body =
                Api.streamBody(
                    () -> open(upload.uri),
                    length,
                    upload.contentType,
                    new Api.Progress() {
                      private int lastStep = -1;

                      @Override
                      public void progress(long written, long total) {
                        int percent = total > 0 ? (int) (written * 100 / total) : -1;
                        int step = percent < 0 ? -1 : percent / 4;
                        if (step == lastStep) return;
                        lastStep = step;
                        main.post(
                            () -> {
                              if (upload.removed) return;
                              upload.percent = percent;
                              notifyWatcher(upload.peer);
                            });
                      }
                    });
            JSONObject result =
                upload.api.upload(
                    upload.peer, upload.filename, upload.contentType, body, handle);
            Attachment attachment = Attachment.from(result);
            if (attachment == null) throw new java.io.IOException("The server sent no attachment.");
            main.post(
                () -> {
                  if (upload.removed) return;
                  upload.attachment = attachment;
                  upload.state = State.DONE;
                  upload.percent = 100;
                  notifyWatcher(upload.peer);
                });
          } catch (Exception e) {
            main.post(
                () -> {
                  if (upload.removed) return;
                  upload.state = State.FAILED;
                  upload.failure = e instanceof Api.Failure ? (Api.Failure) e : null;
                  upload.error =
                      e instanceof Api.Failure
                          ? e.getMessage()
                          : "Upload failed. Tap Retry.";
                  notifyWatcher(upload.peer);
                });
          }
        });
  }

  private InputStream open(Uri uri) throws java.io.IOException {
    InputStream input = app.getContentResolver().openInputStream(uri);
    if (input == null) throw new java.io.IOException("That file could not be opened.");
    return input;
  }

  private void notifyWatcher(String peer) {
    Watcher current = watcher;
    if (current != null) current.uploadsChanged(peer);
  }

  private static final class Cancel {
    static void cancel(Upload upload) {
      try {
        AtomicReference<Call> handle = upload.handle;
        Call call = handle == null ? null : handle.get();
        if (call != null) call.cancel();
      } catch (Exception ignored) {
      }
    }
  }

  static String describe(Upload upload) {
    if (upload.state == State.DONE) return upload.attachment == null ? "Ready" : "Ready to send";
    if (upload.state == State.FAILED)
      return upload.error.isEmpty() ? "Upload failed" : upload.error;
    return upload.percent < 0 ? "Uploading…" : "Uploading " + upload.percent + "%";
  }

  static String percentLabel(Upload upload) {
    if (upload.state == State.DONE) return "Uploaded";
    if (upload.state == State.FAILED) return "Not uploaded";
    return upload.percent < 0 ? "" : String.format(Locale.US, "%d%%", upload.percent);
  }
}
