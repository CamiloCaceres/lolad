package dev.caceres.lolad;

import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.util.LruCache;
import java.io.File;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Downsampled thumbnails for composer chips and message cards. Decoding never
 * touches the main thread, remote bytes are cached on disk by attachment id, and
 * every request carries the view's key so a recycled row simply drops a result
 * that is no longer wanted.
 */
final class ImageCache {
  interface Callback {
    void image(String key, Bitmap bitmap);
  }

  private static final int MEMORY_BYTES = 8 * 1024 * 1024;

  private static ImageCache shared;

  /** Memory and disk thumbnails survive recreation; only a disconnect clears them. */
  static synchronized ImageCache shared(Context context) {
    if (shared == null) shared = new ImageCache(context);
    return shared;
  }

  private final Context app;
  private final File dir;
  private final ExecutorService pool;
  private final Handler main = new Handler(Looper.getMainLooper());
  private final LruCache<String, Bitmap> memory;
  private final Map<String, List<Callback>> waiting = new HashMap<>();
  private final java.util.Set<String> cancelled = java.util.Collections.synchronizedSet(new java.util.HashSet<>());

  ImageCache(Context context) {
    app = context.getApplicationContext();
    dir = new File(app.getCacheDir(), "thumbs");
    pool =
        Executors.newFixedThreadPool(
            2,
            runnable -> {
              Thread thread = new Thread(runnable, "lolad-images");
              thread.setDaemon(true);
              return thread;
            });
    memory =
        new LruCache<>(MEMORY_BYTES) {
          @Override
          protected int sizeOf(String key, Bitmap value) {
            return value.getByteCount();
          }
        };
  }

  static String key(String source, int targetPx) {
    return source + "@" + targetPx;
  }

  void loadLocal(String source, Uri uri, int targetPx, Callback callback) {
    load(key(source, targetPx), targetPx, callback, () -> decodeLocal(uri, targetPx, true));
  }

  void loadRemote(
      Api api, String peer, Attachment attachment, int targetPx, boolean highQuality, Callback callback) {
    if (attachment == null || attachment.id.isEmpty() || !attachment.canInline()) return;
    String source = attachment.cacheKey();
    load(
        key(source, targetPx),
        targetPx,
        callback,
        () -> decodeFile(remoteFile(api, peer, attachment), targetPx, highQuality));
  }

  private void load(String memoryKey, int targetPx, Callback callback, Decoder decoder) {
    cancelled.remove(memoryKey);
    Bitmap cached = memory.get(memoryKey);
    if (cached != null) {
      if (Looper.myLooper() == Looper.getMainLooper()) callback.image(memoryKey, cached);
      else main.post(() -> callback.image(memoryKey, cached));
      return;
    }
    synchronized (waiting) {
      List<Callback> listeners = waiting.get(memoryKey);
      if (listeners != null) {
        listeners.add(callback);
        return;
      }
      listeners = new ArrayList<>(2);
      listeners.add(callback);
      waiting.put(memoryKey, listeners);
    }
    pool.execute(
        () -> {
          Bitmap bitmap = null;
          try {
            if (!cancelled.contains(memoryKey)) bitmap = decoder.decode();
          } catch (Throwable ignored) {
            // A missing or unreadable image is simply not drawn.
          }
          final Bitmap result = bitmap;
          if (result != null) memory.put(memoryKey, result);
          List<Callback> listeners;
          synchronized (waiting) {
            listeners = waiting.remove(memoryKey);
          }
          if (listeners == null) return;
          for (Callback listener : listeners) {
            main.post(() -> listener.image(memoryKey, result));
          }
        });
  }

  private interface Decoder {
    Bitmap decode() throws Exception;
  }

  private Bitmap decodeLocal(Uri uri, int targetPx, boolean highQuality) {
    try {
      BitmapFactory.Options bounds = new BitmapFactory.Options();
      bounds.inJustDecodeBounds = true;
      try (InputStream in = app.getContentResolver().openInputStream(uri)) {
        if (in == null) return null;
        BitmapFactory.decodeStream(in, null, bounds);
      }
      BitmapFactory.Options options = new BitmapFactory.Options();
      options.inSampleSize = sampleSize(bounds.outWidth, bounds.outHeight, targetPx);
      if (!highQuality) options.inPreferredConfig = Bitmap.Config.RGB_565;
      try (InputStream in = app.getContentResolver().openInputStream(uri)) {
        if (in == null) return null;
        return BitmapFactory.decodeStream(in, null, options);
      }
    } catch (Throwable failure) {
      return null;
    }
  }

  private Bitmap decodeFile(File file, int targetPx, boolean highQuality) {
    if (file == null || !file.isFile() || file.length() == 0) return null;
    BitmapFactory.Options bounds = new BitmapFactory.Options();
    bounds.inJustDecodeBounds = true;
    BitmapFactory.decodeFile(file.getPath(), bounds);
    BitmapFactory.Options options = new BitmapFactory.Options();
    options.inSampleSize = sampleSize(bounds.outWidth, bounds.outHeight, targetPx);
    if (!highQuality) options.inPreferredConfig = Bitmap.Config.RGB_565;
    return BitmapFactory.decodeFile(file.getPath(), options);
  }

  private File remoteFile(Api api, String peer, Attachment attachment) throws Exception {
    File file = new File(dir, "att-" + safe(attachment.id) + ".img");
    if (file.isFile() && file.length() > 0) return file;
    if (!dir.isDirectory() && !dir.mkdirs()) return null;
    synchronized (ImageCache.class) {
      if (!file.isFile() || file.length() == 0) {
        api.fetch(peer, attachment.id, true, file, null);
      }
    }
    return file;
  }

  static int sampleSize(int width, int height, int targetPx) {
    if (width <= 0 || height <= 0 || targetPx <= 0) return 1;
    int sample = 1;
    int largest = Math.max(width, height);
    while (largest / (sample * 2) >= targetPx) sample *= 2;
    return sample;
  }

  private static String safe(String id) {
    StringBuilder out = new StringBuilder(id.length());
    for (int i = 0; i < id.length() && out.length() < 96; i++) {
      char c = id.charAt(i);
      out.append(Character.isLetterOrDigit(c) || c == '_' || c == '-' ? c : '_');
    }
    return out.length() == 0 ? String.format(Locale.US, "%08x", id.hashCode()) : out.toString();
  }

  /** Memory is the first thing to give back when the process is under pressure. */
  /** A recycled or rebound row withdraws interest; the fetch is skipped if it has not started. */
  void cancel(String memoryKey) {
    if (memoryKey == null) return;
    cancelled.add(memoryKey);
    synchronized (waiting) {
      waiting.remove(memoryKey);
    }
  }

  void trimMemory() {
    memory.evictAll();
  }

  /** Drops memory and disk copies; called on disconnect. */
  void clear() {
    memory.evictAll();
    synchronized (waiting) {
      waiting.clear();
    }
    File[] files = dir.listFiles();
    if (files != null) for (File file : files) file.delete();
  }

  void shutdown() {
    pool.shutdownNow();
    memory.evictAll();
    synchronized (waiting) {
      waiting.clear();
    }
  }
}
